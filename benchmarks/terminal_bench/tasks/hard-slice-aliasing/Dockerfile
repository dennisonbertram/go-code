FROM go-agent-harness-tb-base:latest

COPY go.mod /app/go.mod
COPY main.go /app/main.go
COPY window.go /app/window.go
COPY window_test.go /app/window_test.go
COPY run-tests.sh /app/run-tests.sh

RUN chmod +x /app/run-tests.sh
