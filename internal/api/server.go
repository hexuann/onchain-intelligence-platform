// Package api 提供查询已采集链上数据与发起资金流分析的 HTTP API。
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"example.com/onchain-intelligence-platform/internal/analysis"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Server struct {
	DB             *pgxpool.Pool
	Analyzer       *analysis.Analyzer
	DefaultChainID int64
}

type transaction struct {
	ChainID     int64     `json:"chain_id"`
	Hash        string    `json:"hash"`
	BlockNumber int64     `json:"block_number"`
	Timestamp   time.Time `json:"timestamp"`
	From        string    `json:"from_address"`
	To          *string   `json:"to_address"`
	Value       string    `json:"value_wei"`
	Gas         string    `json:"gas"`
	GasPrice    string    `json:"gas_price_wei"`
	Status      int64     `json:"status"`
}

// Handler 注册所有路由；核心业务查询直接读取项目一的既有表结构。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := s.DB.Ping(r.Context()); err != nil {
			writeJSON(w, 503, map[string]string{"status": "unavailable"})
			return
		}
		writeJSON(w, 200, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/v1/chains", s.chains)
	mux.HandleFunc("GET /api/v1/blocks/", s.block)
	mux.HandleFunc("GET /api/v1/transactions/", s.tx)
	mux.HandleFunc("GET /api/v1/addresses/", s.addressTransactions)
	mux.HandleFunc("GET /api/v1/stats", s.stats)
	mux.HandleFunc("POST /api/v1/analysis/address-flow", s.createAnalysis)
	mux.HandleFunc("GET /api/v1/analysis/tasks/", s.getTask)
	return mux
}

func (s *Server) chainID(r *http.Request) (int64, error) {
	v := r.URL.Query().Get("chain_id")
	if v == "" {
		return s.DefaultChainID, nil
	}
	return strconv.ParseInt(v, 10, 64)
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func (s *Server) chains(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.Query(r.Context(), `SELECT chain_id, count(*) AS block_count, min(number), max(number) FROM blocks GROUP BY chain_id ORDER BY chain_id`)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "database query failed"})
		return
	}
	defer rows.Close()
	type item struct {
		ChainID     int64  `json:"chain_id"`
		BlockCount  int64  `json:"block_count"`
		FirstBlock  *int64 `json:"first_block"`
		LatestBlock *int64 `json:"latest_block"`
	}
	items := []item{}
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.ChainID, &it.BlockCount, &it.FirstBlock, &it.LatestBlock); err != nil {
			writeJSON(w, 500, map[string]string{"error": "database scan failed"})
			return
		}
		items = append(items, it)
	}
	writeJSON(w, 200, map[string]any{"items": items})
}
func (s *Server) block(w http.ResponseWriter, r *http.Request) {
	id, err := s.chainID(r)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid chain_id"})
		return
	}
	heightText := strings.TrimPrefix(r.URL.Path, "/api/v1/blocks/")
	height, err := strconv.ParseInt(heightText, 10, 64)
	if err != nil || height < 0 {
		writeJSON(w, 400, map[string]string{"error": "invalid block number"})
		return
	}
	var b struct {
		ChainID    int64     `json:"chain_id"`
		Number     int64     `json:"number"`
		Hash       string    `json:"hash"`
		ParentHash string    `json:"parent_hash"`
		Timestamp  time.Time `json:"timestamp"`
		TxCount    int64     `json:"transaction_count"`
	}
	err = s.DB.QueryRow(r.Context(), `SELECT b.chain_id,b.number,b.hash,b.parent_hash,b.timestamp,count(t.hash) FROM blocks b LEFT JOIN transactions t ON t.chain_id=b.chain_id AND t.block_number=b.number WHERE b.chain_id=$1 AND b.number=$2 GROUP BY b.chain_id,b.number`, id, height).Scan(&b.ChainID, &b.Number, &b.Hash, &b.ParentHash, &b.Timestamp, &b.TxCount)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, 404, map[string]string{"error": "block not found"})
		return
	}
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "database query failed"})
		return
	}
	writeJSON(w, 200, b)
}
func (s *Server) tx(w http.ResponseWriter, r *http.Request) {
	id, err := s.chainID(r)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid chain_id"})
		return
	}
	hash := strings.TrimPrefix(r.URL.Path, "/api/v1/transactions/")
	var t transaction
	err = s.DB.QueryRow(r.Context(), `SELECT t.chain_id,t.hash,t.block_number,b.timestamp,t.from_address,t.to_address,t.value::text,t.gas::text,t.gas_price::text,t.status FROM transactions t JOIN blocks b ON b.chain_id=t.chain_id AND b.number=t.block_number WHERE t.chain_id=$1 AND lower(t.hash)=lower($2)`, id, hash).Scan(&t.ChainID, &t.Hash, &t.BlockNumber, &t.Timestamp, &t.From, &t.To, &t.Value, &t.Gas, &t.GasPrice, &t.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, 404, map[string]string{"error": "transaction not found"})
		return
	}
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "database query failed"})
		return
	}
	writeJSON(w, 200, t)
}
func (s *Server) addressTransactions(w http.ResponseWriter, r *http.Request) {
	// 路径格式为 /api/v1/addresses/{address}/transactions。
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 5 || parts[4] != "transactions" {
		http.NotFound(w, r)
		return
	}
	address := strings.ToLower(parts[3])
	if !evmAddressPattern.MatchString(address) {
		writeJSON(w, 400, map[string]string{"error": "invalid EVM address"})
		return
	}
	id, err := s.chainID(r)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid chain_id"})
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit == 0 {
		limit = 20
	}
	if limit < 1 || limit > 100 {
		writeJSON(w, 400, map[string]string{"error": "limit must be between 1 and 100"})
		return
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		writeJSON(w, 400, map[string]string{"error": "offset must be non-negative"})
		return
	}
	rows, err := s.DB.Query(r.Context(), `SELECT t.chain_id,t.hash,t.block_number,b.timestamp,t.from_address,t.to_address,t.value::text,t.gas::text,t.gas_price::text,t.status FROM transactions t JOIN blocks b ON b.chain_id=t.chain_id AND b.number=t.block_number WHERE t.chain_id=$1 AND (lower(t.from_address)=$2 OR lower(COALESCE(t.to_address,''))=$2) ORDER BY t.block_number DESC LIMIT $3 OFFSET $4`, id, address, limit, offset)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "database query failed"})
		return
	}
	defer rows.Close()
	items := []transaction{}
	for rows.Next() {
		var t transaction
		if err := rows.Scan(&t.ChainID, &t.Hash, &t.BlockNumber, &t.Timestamp, &t.From, &t.To, &t.Value, &t.Gas, &t.GasPrice, &t.Status); err != nil {
			writeJSON(w, 500, map[string]string{"error": "database scan failed"})
			return
		}
		items = append(items, t)
	}
	writeJSON(w, 200, map[string]any{"items": items, "limit": limit, "offset": offset})
}
func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	id, err := s.chainID(r)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid chain_id"})
		return
	}
	var out struct {
		ChainID      int64  `json:"chain_id"`
		Blocks       int64  `json:"blocks"`
		Transactions int64  `json:"transactions"`
		EventLogs    int64  `json:"event_logs"`
		FirstBlock   *int64 `json:"first_block"`
		LatestBlock  *int64 `json:"latest_block"`
	}
	out.ChainID = id
	err = s.DB.QueryRow(r.Context(), `SELECT (SELECT count(*) FROM blocks WHERE chain_id=$1),(SELECT count(*) FROM transactions WHERE chain_id=$1),(SELECT count(*) FROM event_logs WHERE chain_id=$1),(SELECT min(number) FROM blocks WHERE chain_id=$1),(SELECT max(number) FROM blocks WHERE chain_id=$1)`, id).Scan(&out.Blocks, &out.Transactions, &out.EventLogs, &out.FirstBlock, &out.LatestBlock)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "database query failed"})
		return
	}
	writeJSON(w, 200, out)
}

var evmAddressPattern = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)

type createAnalysisRequest struct {
	ChainID int64  `json:"chain_id"`
	Address string `json:"address"`
	Depth   int    `json:"depth"`
}

func (s *Server) createAnalysis(w http.ResponseWriter, r *http.Request) {
	var req createAnalysisRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid JSON body"})
		return
	}
	if req.ChainID == 0 {
		req.ChainID = s.DefaultChainID
	}
	if req.Depth == 0 {
		req.Depth = 1
	}
	req.Address = strings.ToLower(strings.TrimSpace(req.Address))
	if req.ChainID <= 0 || !evmAddressPattern.MatchString(req.Address) || req.Depth < 1 || req.Depth > 5 {
		writeJSON(w, 400, map[string]string{"error": "chain_id must be positive, address must be a 0x-prefixed 40-hex-character EVM address, and depth must be between 1 and 5"})
		return
	}
	var taskID int64
	err := s.DB.QueryRow(r.Context(), `INSERT INTO analysis_tasks(chain_id,address,depth,status) VALUES($1,$2,$3,'pending') RETURNING id`, req.ChainID, req.Address, req.Depth).Scan(&taskID)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "could not create analysis task"})
		return
	}
	// 分析放到后台执行，避免长时间占用 HTTP 请求；任务结果可通过 GET 接口查询。
	go s.runTask(taskID, req)
	writeJSON(w, http.StatusAccepted, map[string]any{"task_id": taskID, "status": "pending"})
}
func (s *Server) runTask(taskID int64, req createAnalysisRequest) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	_, _ = s.DB.Exec(ctx, `UPDATE analysis_tasks SET status='running',updated_at=now() WHERE id=$1`, taskID)
	result, err := s.Analyzer.Run(ctx, req.ChainID, req.Address, req.Depth)
	if err != nil {
		_, _ = s.DB.Exec(ctx, `UPDATE analysis_tasks SET status='failed',error=$2,updated_at=now() WHERE id=$1`, taskID, err.Error())
		return
	}
	payload, err := json.Marshal(result)
	if err != nil {
		_, _ = s.DB.Exec(ctx, `UPDATE analysis_tasks SET status='failed',error=$2,updated_at=now() WHERE id=$1`, taskID, err.Error())
		return
	}
	_, _ = s.DB.Exec(ctx, `UPDATE analysis_tasks SET status='completed',result=$2::jsonb,updated_at=now() WHERE id=$1`, taskID, string(payload))
}
func (s *Server) getTask(w http.ResponseWriter, r *http.Request) {
	idText := strings.TrimPrefix(r.URL.Path, "/api/v1/analysis/tasks/")
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil || id < 1 {
		writeJSON(w, 400, map[string]string{"error": "invalid task id"})
		return
	}
	var task struct {
		ID        int64           `json:"id"`
		ChainID   int64           `json:"chain_id"`
		Address   string          `json:"address"`
		Depth     int             `json:"depth"`
		Status    string          `json:"status"`
		Result    json.RawMessage `json:"result,omitempty"`
		Error     string          `json:"error,omitempty"`
		CreatedAt time.Time       `json:"created_at"`
		UpdatedAt time.Time       `json:"updated_at"`
	}
	err = s.DB.QueryRow(r.Context(), `SELECT id,chain_id,address,depth,status,result,error,created_at,updated_at FROM analysis_tasks WHERE id=$1`, id).Scan(&task.ID, &task.ChainID, &task.Address, &task.Depth, &task.Status, &task.Result, &task.Error, &task.CreatedAt, &task.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, 404, map[string]string{"error": "task not found"})
		return
	}
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "database query failed"})
		return
	}
	writeJSON(w, 200, task)
}
