-- +goose Up
-- quote_cleanup deletes expired fare quotes that no order references. Both its
-- NOT EXISTS check and PostgreSQL's FK check on every deleted quote look up
-- orders by quote_id; without this index each lookup scans the orders table.
CREATE INDEX orders_quote_idx ON orders (quote_id) WHERE quote_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS orders_quote_idx;
