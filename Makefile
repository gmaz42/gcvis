
run: build
	clear
	exec bin/gcvis godoc -index -http=:6060

build:
	go build -o bin/gcvis .
.PHONY: build
