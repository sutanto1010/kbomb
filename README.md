# kbomb

CLI tool to stress-test Kafka producers with configurable throughput, concurrency, message payloads, and delivery semantics.

## Install
- Build locally: `go build .`
- Install to `GOBIN`: `go install .`

## Quick Start
- Send 10k messages to `my-topic` on local Kafka:
```
kbomb -topic my-topic -count 10000
```
- Use multiple brokers:
```
kbomb -brokers localhost:9092,localhost:9093 -topic my-topic -count 10000
```
- Load JSON payload from file:
```
kbomb -topic my-topic -body-file ./payload.json -count 5000
```
- Rate limit to 5k msg/s with higher concurrency:
```
kbomb -topic my-topic -count 20000 -rate 5000 -concurrency 32
```
- Use compression and leader acks:
```
kbomb -topic my-topic -compression zstd -acks leader -count 10000
```
- Add headers:
```
kbomb -topic my-topic -headers env=prod,trace=abc123 -count 1000
```
- Random keys:
```
kbomb -topic my-topic -random-key -count 1000
```

## Flags
- `-brokers`: comma-separated broker list, e.g. `host:port,host:port` (default: `localhost:9092`)
- `-topic`: target topic (required)
- `-count`: total messages to send (default: `1000`)
- `-concurrency`: worker goroutines (default: CPU count)
- `-rate`: global messages/sec; `0` for unlimited
- `-key`: static key to use for all messages
- `-random-key`: generate random hex keys
- `-compression`: `none|gzip|snappy|lz4|zstd` (default: `none`)
- `-acks`: `none|leader|all` (default: `all`)
- `-headers`: comma-separated `k=v` pairs
- `-body`: inline body string
- `-body-file`: path to JSON file used as message value
- `-batch-size`: writer batch size (default: `100`)
- `-batch-timeout-ms`: writer batch timeout in ms (default: `10`)
- `-async`: fire-and-forget writes
- `-balancer`: `leastbytes|roundrobin|hash|crc32` (default: `leastbytes`)

## Output
- Prints progress every second:
  - `sent`, `failed`, current `rate` in msg/s, `elapsed`
- Final summary:
  - `brokers`, `topic`, `count`, `sent`, `failed`, `elapsed`

## Notes
- Topic creation: ensure your Kafka allows auto-create or pre-create the topic.
- `-acks none` with `-async` gives highest throughput while ignoring errors.
- `-compression zstd` typically offers good size savings with modern Kafka versions.
- `-balancer` controls partitioning; `leastbytes` optimizes for broker load.

## License
See `LICENSE`.
