-- +goose Up
CREATE TABLE IF NOT EXISTS users
(
    telegram_id           bigint PRIMARY KEY,
    username              varchar(255),
    level                 varchar(10),
    onenote_access_token  text,
    onenote_refresh_token text,
    onenote_expires_at    timestamp,
    onenote_notebook_id   varchar(255),
    onenote_section_id    varchar(255),
    use_manual_pages      boolean     DEFAULT FALSE,
    reminder_time         varchar(10) DEFAULT '09:00',
    created_at            timestamp   DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS page_references
(
    page_id     varchar(255) NOT NULL,
    user_id     bigint       NOT NULL,
    title       text         NOT NULL,
    page_number integer,
    category    varchar(50),
    level       varchar(10),
    source      varchar(50),
    created_at  timestamp DEFAULT NOW(),
    last_synced timestamp,
    PRIMARY KEY (page_id, user_id),
    FOREIGN KEY (user_id) REFERENCES users (telegram_id)
        ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS user_progress
(
    user_id          bigint       NOT NULL,
    page_id          varchar(255) NOT NULL,
    repetition_count integer DEFAULT 0,
    last_review_date timestamp,
    next_review_date timestamp,
    interval_days    integer,
    success_rate     integer,
    history          jsonb   DEFAULT '[]'::jsonb,
    PRIMARY KEY (user_id, page_id),
    FOREIGN KEY (user_id) REFERENCES users (telegram_id)
        ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_next_review_date ON user_progress (user_id, next_review_date);
CREATE INDEX IF NOT EXISTS idx_page_number ON page_references (user_id, page_number);

-- +goose Down