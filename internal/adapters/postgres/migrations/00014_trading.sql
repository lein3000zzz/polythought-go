-- +goose Up
-- A workflow's paper money (D89), opened by its first paper order.
CREATE TABLE paper_accounts (
    workflow_id  uuid PRIMARY KEY REFERENCES workflows (id) ON DELETE CASCADE,
    owner_id     uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    cash         numeric     NOT NULL CHECK (cash >= 0),
    started_with numeric     NOT NULL CHECK (started_with > 0),
    started_at   timestamptz NOT NULL
);

-- The ledger: every order a workflow placed, paper or live, and how it went.
CREATE TABLE orders (
    id           uuid PRIMARY KEY,
    workflow_id  uuid        NOT NULL REFERENCES workflows (id) ON DELETE CASCADE,
    owner_id     uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    execution_id uuid REFERENCES executions (id) ON DELETE SET NULL,
    node_id      text        NOT NULL,
    mode         text        NOT NULL CHECK (mode IN ('paper', 'live')),
    trader       text        NOT NULL DEFAULT '', -- the wallet copied; '' for none (D92)
    trader_label text        NOT NULL DEFAULT '',
    token_id     text        NOT NULL,
    market_id    text        NOT NULL DEFAULT '',
    side         text        NOT NULL CHECK (side IN ('buy', 'sell')),
    order_type   text        NOT NULL CHECK (order_type IN ('FOK', 'FAK', 'GTC', 'GTD')),
    post_only    boolean     NOT NULL DEFAULT false,
    price        numeric     NOT NULL, -- the limit
    shares       numeric     NOT NULL DEFAULT 0,
    dollars      numeric     NOT NULL DEFAULT 0, -- a buy of so many dollars, shares 0
    expires_at   timestamptz,
    status       text        NOT NULL CHECK (status IN ('filled', 'partial', 'unfilled', 'refused')),
    reason       text        NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL
);

CREATE INDEX orders_by_workflow ON orders (workflow_id, created_at DESC, id DESC);

-- Each order's matches, a price level each.
CREATE TABLE order_fills (
    order_id uuid    NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    seq      integer NOT NULL,
    price    numeric NOT NULL,
    shares   numeric NOT NULL CHECK (shares > 0),
    dollars  numeric NOT NULL,
    fee      numeric NOT NULL CHECK (fee >= 0),
    PRIMARY KEY (order_id, seq)
);

-- A workflow's position per mode, trader copied and outcome (D92), as its orders left it.
CREATE TABLE holdings (
    workflow_id  uuid        NOT NULL REFERENCES workflows (id) ON DELETE CASCADE,
    mode         text        NOT NULL CHECK (mode IN ('paper', 'live')),
    trader       text        NOT NULL DEFAULT '',
    trader_label text        NOT NULL DEFAULT '',
    token_id     text        NOT NULL,
    market_id    text        NOT NULL DEFAULT '',
    shares       numeric     NOT NULL CHECK (shares >= 0),
    cost         numeric     NOT NULL,
    realized     numeric     NOT NULL,
    updated_at   timestamptz NOT NULL,
    PRIMARY KEY (workflow_id, mode, trader, token_id)
);

-- +goose Down
DROP TABLE holdings;
DROP TABLE order_fills;
DROP TABLE orders;
DROP TABLE paper_accounts;
