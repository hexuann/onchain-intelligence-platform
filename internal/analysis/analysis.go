// Package analysis 实现基于已采集交易数据的地址资金流关系分析。
package analysis

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Edge 表示一条原生币转账关系。金额 value 使用十进制字符串，避免浮点数精度损失。
type Edge struct {
	ChainID     int64  `json:"chain_id"`
	TxHash      string `json:"tx_hash"`
	BlockNumber int64  `json:"block_number"`
	From        string `json:"from"`
	To          string `json:"to"`
	Value       string `json:"value_wei"`
}

// Result 是地址关系分析的结构化结果。
type Result struct {
	ChainID     int64    `json:"chain_id"`
	RootAddress string   `json:"root_address"`
	Depth       int      `json:"depth"`
	Addresses   []string `json:"addresses"`
	Edges       []Edge   `json:"edges"`
	Note        string   `json:"note"`
}

// Analyzer 从项目一写入的 transactions 表中遍历原生币转账关系。
type Analyzer struct{ DB *pgxpool.Pool }

// Run 以 root 为起点，按深度优先扩展地址关系。每个地址最多读取 100 条相关交易，限制资源消耗。
func (a *Analyzer) Run(ctx context.Context, chainID int64, root string, depth int) (Result, error) {
	if a.DB == nil {
		return Result{}, fmt.Errorf("database pool is required")
	}
	root = strings.ToLower(strings.TrimSpace(root))
	if root == "" {
		return Result{}, fmt.Errorf("address is required")
	}
	if depth < 1 || depth > 5 {
		return Result{}, fmt.Errorf("depth must be between 1 and 5")
	}

	result := Result{ChainID: chainID, RootAddress: root, Depth: depth,
		Addresses: []string{}, Edges: []Edge{},
		Note: "当前版本基于 transactions.value 分析原生币直接转账；合约内部转账及 ERC-20 转账需另行解析事件日志或调用追踪。"}
	visited := map[string]bool{root: true}
	frontier := []string{root}
	edgeSeen := map[string]bool{}

	for level := 0; level < depth && len(frontier) > 0; level++ {
		nextSet := map[string]bool{}
		for _, address := range frontier {
			// from/to 任一端命中当前地址即可视为相关转账。
			rows, err := a.DB.Query(ctx, `
				SELECT chain_id, hash, block_number, lower(from_address), lower(COALESCE(to_address,'')), value::text
				FROM transactions
				WHERE chain_id=$1 AND value > 0
				  AND (lower(from_address)=$2 OR lower(COALESCE(to_address,''))=$2)
				ORDER BY block_number DESC LIMIT 100`, chainID, address)
			if err != nil {
				return result, err
			}
			for rows.Next() {
				var edge Edge
				if err := rows.Scan(&edge.ChainID, &edge.TxHash, &edge.BlockNumber, &edge.From, &edge.To, &edge.Value); err != nil {
					rows.Close()
					return result, err
				}
				key := edge.TxHash
				if !edgeSeen[key] {
					result.Edges = append(result.Edges, edge)
					edgeSeen[key] = true
				}
				for _, candidate := range []string{edge.From, edge.To} {
					if candidate != "" && !visited[candidate] {
						nextSet[candidate] = true
					}
				}
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return result, err
			}
			rows.Close()
		}
		frontier = frontier[:0]
		nextAddresses := make([]string, 0, len(nextSet))
		for address := range nextSet {
			nextAddresses = append(nextAddresses, address)
		}
		sort.Strings(nextAddresses)
		for _, address := range nextAddresses {
			if !visited[address] {
				visited[address] = true
				frontier = append(frontier, address)
			}
		}
	}
	for address := range visited {
		result.Addresses = append(result.Addresses, address)
	}
	sort.Strings(result.Addresses)
	return result, nil
}
