package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/compress"
)

type strSlice []string

func (s *strSlice) String() string {
	return strings.Join(*s, ",")
}

func (s *strSlice) Set(v string) error {
	*s = strings.Split(v, ",")
	return nil
}

func parseHeaders(h string) []kafka.Header {
	if h == "" {
		return nil
	}
	parts := strings.Split(h, ",")
	headers := make([]kafka.Header, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			continue
		}
		kv := strings.SplitN(p, "=", 2)
		if len(kv) != 2 {
			continue
		}
		headers = append(headers, kafka.Header{Key: strings.TrimSpace(kv[0]), Value: []byte(kv[1])})
	}
	return headers
}

func compressionFromString(s string) compress.Compression {
	switch strings.ToLower(s) {
	case "gzip":
		return compress.Gzip
	case "snappy":
		return compress.Snappy
	case "lz4":
		return compress.Lz4
	case "zstd":
		return compress.Zstd
	default:
		return compress.None
	}
}

func acksFromString(s string) kafka.RequiredAcks {
	switch strings.ToLower(s) {
	case "none":
		return kafka.RequireNone
	case "leader", "one":
		return kafka.RequireOne
	case "all":
		return kafka.RequireAll
	default:
		return kafka.RequireAll
	}
}

func balancerFromString(s string) kafka.Balancer {
	switch strings.ToLower(s) {
	case "roundrobin", "rr":
		return &kafka.RoundRobin{}
	case "hash":
		return &kafka.Hash{}
	case "crc32":
		return kafka.CRC32Balancer{}
	default:
		return &kafka.LeastBytes{}
	}
}

func randomKey(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	out := make([]byte, hex.EncodedLen(len(b)))
	hex.Encode(out, b)
	return out
}

func readBody(body string, bodyFile string) ([]byte, error) {
	if bodyFile != "" {
		info, err := os.Stat(bodyFile)
		if err != nil {
			return nil, err
		}
		if info.IsDir() {
			return nil, errors.New("body-file points to a directory")
		}
		return os.ReadFile(bodyFile)
	}
	return []byte(body), nil
}

func main() {
	var brokers strSlice
	var topic string
	var count int
	var concurrency int
	var rate int
	var keyStatic string
	var useRandomKey bool
	var compression string
	var acks string
	var headersArg string
	var body string
	var bodyFile string
	var batchSize int
	var batchTimeoutMs int
	var async bool
	var balancer string

	flag.Var(&brokers, "brokers", "Comma-separated list of broker addresses (host:port)")
	flag.StringVar(&topic, "topic", "", "Kafka topic to produce to")
	flag.IntVar(&count, "count", 1000, "Total number of messages to send")
	flag.IntVar(&concurrency, "concurrency", runtime.NumCPU(), "Number of concurrent workers")
	flag.IntVar(&rate, "rate", 0, "Target messages per second (global), 0 for unlimited")
	flag.StringVar(&keyStatic, "key", "", "Static key for all messages")
	flag.BoolVar(&useRandomKey, "random-key", false, "Generate random hex keys")
	flag.StringVar(&compression, "compression", "none", "Compression codec: none,gzip,snappy,lz4,zstd")
	flag.StringVar(&acks, "acks", "all", "Acknowledgement policy: none,leader,all")
	flag.StringVar(&headersArg, "headers", "", "Headers as k=v,k2=v2")
	flag.StringVar(&body, "body", "", "Message body string")
	flag.StringVar(&bodyFile, "body-file", "", "Path to JSON file used as message body")
	flag.IntVar(&batchSize, "batch-size", 1, "Writer batch size")
	flag.IntVar(&batchTimeoutMs, "batch-timeout-ms", 10, "Writer batch timeout in ms")
	flag.BoolVar(&async, "async", false, "Use async writes (fire-and-forget)")
	flag.StringVar(&balancer, "balancer", "leastbytes", "Partition balancer: leastbytes,roundrobin,hash,crc32")
	flag.Parse()

	if len(brokers) == 0 {
		brokers = strSlice{"localhost:9092"}
	}
	if topic == "" {
		log.Fatal("topic is required")
	}
	if count < 1 {
		log.Fatal("count must be >= 1")
	}
	if concurrency < 1 {
		concurrency = 1
	}

	bodyBytes, err := readBody(body, bodyFile)
	if err != nil {
		log.Fatalf("failed to read body: %v", err)
	}

	headers := parseHeaders(headersArg)
	comp := compressionFromString(compression)
	acksVal := acksFromString(acks)
	bal := balancerFromString(balancer)

	writer := &kafka.Writer{
		Addr:                   kafka.TCP(brokers...),
		Topic:                  topic,
		Balancer:               bal,
		RequiredAcks:           acksVal,
		Async:                  async,
		Compression:            comp,
		BatchSize:              batchSize,
		BatchTimeout:           time.Duration(batchTimeoutMs) * time.Millisecond,
		AllowAutoTopicCreation: false,
		BatchBytes:             10 * 1024 * 1024,
	}
	defer writer.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, os.Interrupt)
	go func() {
		<-sigc
		cancel()
	}()

	var sent int64
	var failed int64

	var keyFunc func(i int) []byte
	if useRandomKey {
		keyFunc = func(i int) []byte { return randomKey(8) }
	} else if keyStatic != "" {
		keyFunc = func(i int) []byte { return []byte(keyStatic) }
	} else {
		keyFunc = func(i int) []byte { return []byte(fmt.Sprintf("%d", i)) }
	}

	var limiter <-chan time.Time
	if rate > 0 {
		interval := time.Second / time.Duration(rate)
		if interval <= 0 {
			interval = time.Nanosecond
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		limiter = ticker.C
	}

	start := time.Now()
	wg := sync.WaitGroup{}
	msgCh := make(chan int, batchSize*concurrency)

	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range msgCh {
				if limiter != nil {
					select {
					case <-limiter:
					case <-ctx.Done():
						return
					}
				}
				msg := kafka.Message{
					Key:     keyFunc(idx),
					Value:   bodyBytes,
					Headers: headers,
					Time:    time.Now(),
				}
				err := writer.WriteMessages(ctx, msg)
				if err != nil {
					atomic.AddInt64(&failed, 1)
					if !async {
						log.Printf("write error: %v", err)
					}
					continue
				}
				atomic.AddInt64(&sent, 1)
			}
		}()
	}

	go func() {
		t := time.NewTicker(1 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				elapsed := time.Since(start).Seconds()
				s := atomic.LoadInt64(&sent)
				f := atomic.LoadInt64(&failed)
				rateCur := float64(0)
				if elapsed > 0 {
					rateCur = float64(s) / elapsed
				}
				fmt.Printf("sent=%d failed=%d rate=%.0f msg/s elapsed=%s\n", s, f, rateCur, time.Since(start).Truncate(time.Second))
			}
		}
	}()

	for i := 0; i < count; i++ {
		select {
		case <-ctx.Done():
			break
		case msgCh <- i:
		}
	}
	close(msgCh)
	wg.Wait()

	elapsed := time.Since(start)
	fmt.Printf("done brokers=%s topic=%s count=%d sent=%d failed=%d elapsed=%s\n",
		strings.Join(brokers, ","), topic, count, atomic.LoadInt64(&sent), atomic.LoadInt64(&failed), elapsed.Truncate(time.Millisecond))

	_ = filepath.Base(os.Args[0])
}
