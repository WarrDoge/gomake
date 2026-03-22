BUILD_DIR = build
BIN = $(BUILD_DIR)/bin/demo

.PHONY: all verify clean

all: verify

$(BIN): go.mod cmd/demo/main.go
	mkdir -p $(BUILD_DIR)/bin
	go build -o $@ ./cmd/demo

verify: $(BIN)
	./$< > $(BUILD_DIR)/demo.txt
	grep -Fx "hello from go" $(BUILD_DIR)/demo.txt

clean:
	rm -rf $(BUILD_DIR)
