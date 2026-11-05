package indexer

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

type Log struct {
	Address string   `json:"address"`
	Topics  []string `json:"topics"`
	Data    string   `json:"data"`
	Index   uint64   `json:"-"`
}

type Transaction struct {
	Hash     string `json:"hash"`
	From     string `json:"from"`
	To       string `json:"to"`
	Value    string `json:"value"`
	Gas      string `json:"gas"`
	GasPrice string `json:"gasPrice"`
	BlockNum uint64 `json:"-"`
	Status   uint64 `json:"-"`
	Logs     []Log  `json:"-"`
}

type Block struct {
	Number       uint64        `json:"-"`
	Hash         string        `json:"hash"`
	ParentHash   string        `json:"parentHash"`
	TimestampHex string        `json:"timestamp"`
	Transactions []Transaction `json:"transactions"`
}

type Source interface {
	LatestBlock(context.Context) (uint64, error)
	BlockWithTransactions(context.Context, uint64) (Block, error)
	Receipt(context.Context, string) (Receipt, error)
}

type Receipt struct {
	TransactionHash string `json:"transactionHash"`
	StatusHex       string `json:"status"`
	Logs            []Log  `json:"logs"`
}

type Repository interface {
	Checkpoint(context.Context, uint64) (uint64, error) // returns the next block to process
	SaveBlock(context.Context, Block) error
}

type Config struct {
	StartBlock uint64
	EndBlock   uint64 // inclusive
	Latest     bool   // use node latest block instead of EndBlock
	Workers    int
}

func (l *Log) UnmarshalJSON(data []byte) error {
	var raw struct {
		Address string   `json:"address"`
		Topics  []string `json:"topics"`
		Data    string   `json:"data"`
		Index   string   `json:"logIndex"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	l.Address = raw.Address
	l.Topics = raw.Topics
	l.Data = raw.Data
	s := strings.TrimPrefix(raw.Index, "0x")
	n, err := strconv.ParseUint(s, 16, 64)
	if err != nil {
		return fmt.Errorf("invalid logIndex %q: %w", raw.Index, err)
	}
	l.Index = n
	return nil
}
