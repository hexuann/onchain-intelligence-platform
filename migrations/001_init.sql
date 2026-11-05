CREATE TABLE IF NOT EXISTS blocks (
  chain_id BIGINT NOT NULL, number BIGINT NOT NULL, hash TEXT NOT NULL, parent_hash TEXT NOT NULL,
  timestamp TIMESTAMPTZ NOT NULL, PRIMARY KEY(chain_id, number)
);
CREATE TABLE IF NOT EXISTS transactions (
  chain_id BIGINT NOT NULL, hash TEXT NOT NULL, block_number BIGINT NOT NULL,
  from_address TEXT NOT NULL, to_address TEXT, value NUMERIC(78,0) NOT NULL DEFAULT 0,
  gas NUMERIC(78,0) NOT NULL DEFAULT 0, gas_price NUMERIC(78,0) NOT NULL DEFAULT 0,
  status BIGINT NOT NULL DEFAULT 0, PRIMARY KEY(chain_id, hash),
  FOREIGN KEY(chain_id, block_number) REFERENCES blocks(chain_id, number) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS event_logs (
  chain_id BIGINT NOT NULL, tx_hash TEXT NOT NULL, log_index BIGINT NOT NULL,
  address TEXT NOT NULL, topics TEXT[] NOT NULL DEFAULT '{}', data TEXT NOT NULL,
  block_number BIGINT NOT NULL, PRIMARY KEY(chain_id, tx_hash, log_index),
  FOREIGN KEY(chain_id, tx_hash) REFERENCES transactions(chain_id, hash) ON DELETE CASCADE
);

-- Indexes supporting per-chain address history and native-transfer traversal.
CREATE INDEX IF NOT EXISTS idx_transactions_chain_from_lower_block
    ON transactions (chain_id, lower(from_address), block_number DESC);
CREATE INDEX IF NOT EXISTS idx_transactions_chain_to_lower_block
    ON transactions (chain_id, lower(to_address), block_number DESC);
CREATE INDEX IF NOT EXISTS idx_event_logs_chain_block
    ON event_logs (chain_id, block_number);
