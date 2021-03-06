package epee

import (
	"errors"
	"fmt"
	"github.com/Shopify/sarama"
	"time"
)

var (
	ErrDecodingMessageFailed = errors.New("message decoding failed")
	ErrNotFound              = errors.New("not found")
	ErrStreamClosing         = errors.New("stream closing")
)

const (
	RetryForever = 0
)

// Must open a Zookeeper connection within retry times. If retry <= 0, it will
// retry for forever.
func MustGetZookeeperClient(servers []string, retry int) ZookeeperClient {
	var client ZookeeperClient
	attempts := 0

	for {
		var err error

		client, err = NewZookeeperClient(servers)

		// Increment retry if need be.
		if retry > 0 {
			attempts += 1
		}

		if err != nil && attempts > retry {
			panic(err)
		} else if err != nil {
			<-time.After(3 * time.Second)
		} else {
			// We found it, we're good!
			break
		}
	}

	return client
}

func findRegisteredBrokers(zk ZookeeperClient) ([]string, error) {
	paths, err := zk.List("/brokers/ids")

	if err != nil {
		return []string{}, err
	}

	fullPaths := make([]string, 0)

	for _, p := range paths {
		data := make(map[string]interface{})
		err := zk.Get(p, data)

		if err != nil {
			return []string{}, err
		}

		fullPaths = append(fullPaths, fmt.Sprintf("%s:%0.0f", data["host"], data["port"]))
	}

	return fullPaths, nil
}

func getConfig(clientID string) *sarama.Config {
	config := sarama.NewConfig()
	config.Producer.Compression = sarama.CompressionSnappy
	config.ClientID = clientID
	config.Producer.Partitioner = func(topic string) sarama.Partitioner {
		return sarama.NewHashPartitioner(topic)
	}

	return config
}
