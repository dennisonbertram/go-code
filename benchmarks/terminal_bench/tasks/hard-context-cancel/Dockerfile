FROM go-agent-harness-tb-base:latest

COPY go.mod /app/go.mod
COPY main.go /app/main.go
COPY pool.go /app/pool.go
COPY pool_test.go /app/pool_test.go
COPY run-tests.sh /app/run-tests.sh

RUN chmod +x /app/run-tests.sh
