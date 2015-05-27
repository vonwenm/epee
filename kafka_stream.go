package epee

import (
	"errors"
	"github.com/Shopify/sarama"
	"log"
	"sync"
	"time"
)

var (
	ErrStreamClosing = errors.New("stream closing")
)

type KafkaStream interface {
	// Close all resources associated with this thing.
	Close()

	// Returns a channel of messages to consume based on the client ID.
	Consume(topic string, partition int, offset int64) (*StreamConsumer, error)

	// Given a consumer, gracefully stops it.
	CancelConsumer(*StreamConsumer) error
}

type kafkaStreamImpl struct {
	sync.Mutex

	// A list of stream consumers that have been created.
	consumers map[*StreamConsumer]bool

	// Indicates to child processes that we should continue running.
	closing bool

	// The consumer we're using to consume stuff.
	consumer sarama.Consumer

	// The client connected to
	client sarama.Client

	// The zookeeper cluster our service is connecting to.
	zk ZookeeperClient
}

func (ks *kafkaStreamImpl) Consume(topic string, partition int, offset int64) (*StreamConsumer, error) {
	// If the stream is in the process of closing we don't want to start a new
	// consumer.
	if ks.closing {
		return nil, ErrStreamClosing
	}

	if offset == 0 {
		offset = sarama.OffsetOldest
	}

	var err error
	var partitionConsumer sarama.PartitionConsumer

	for {
		if partitionConsumer != nil {
			break
		}

		partitionConsumer, err = ks.consumer.ConsumePartition(topic, int32(partition), offset)

		if err == sarama.ErrUnknownTopicOrPartition {
			log.Printf("WARNING: Failed to find [%s, partition %d]. Waiting, then retrying.", topic, partition)
			<-time.After(5 * time.Second)
			continue
		} else if err != nil {
			log.Printf("ERROR: Failed to start partition consumer. %v", err)
			return nil, err
		}
	}

	ch := make(chan Message, 0)
	consumer := NewStreamConsumer(ch, partitionConsumer)

	// We have to acquire the lock to modify the map.
	ks.Lock()
	ks.consumers[consumer] = true
	ks.Unlock()

	// Let's start the consumer up!
	consumer.Start()

	return consumer, nil
}

func (ks *kafkaStreamImpl) CancelConsumer(sc *StreamConsumer) error {
	ks.Lock()
	defer ks.Unlock()

	// Only actually cancel this consumer if it's still alive.
	_, ok := ks.consumers[sc]

	if ok {
		sc.Close()
		delete(ks.consumers, sc)
	}

	return nil
}

func (ks *kafkaStreamImpl) Close() {
	ks.Lock()
	defer ks.Unlock()

	ks.closing = true

	// Let's close all the created consumers.
	for c := range ks.consumers {
		// Wait for this consumer to close fully.
		c.Close()
	}

	// Now all of the consumers should (theoretically) be done.
	ks.consumer.Close()
}

func NewKafkaStream(clientID string, zk ZookeeperClient) (KafkaStream, error) {
	brokers, err := findRegisteredBrokers(zk)

	if err != nil {
		return nil, err
	}

	// TODO: Any options for Sarama?
	config := sarama.NewConfig()
	config.ClientID = clientID

	client, err := sarama.NewClient(brokers, config)

	if err != nil {
		return nil, err
	}

	// Now that we have a client, let's start a consumer up.
	consumer, err := sarama.NewConsumerFromClient(client)

	if err != nil {
		return nil, err
	}

	stream := new(kafkaStreamImpl)
	stream.client = client
	stream.consumer = consumer
	stream.consumers = make(map[*StreamConsumer]bool)

	return stream, nil
}