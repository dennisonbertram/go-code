FROM go-agent-harness-tb-base:latest

COPY go.mod /app/go.mod
COPY main.go /app/main.go
COPY topo.go /app/topo.go
COPY topo_test.go /app/topo_test.go
COPY run-tests.sh /app/run-tests.sh

RUN chmod +x /app/run-tests.sh
