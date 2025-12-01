-- +goose Up
CREATE TABLE IF NOT EXISTS users (
    telegram_id bigint PRIMARY KEY,
    username varchar(255),
    level varchar(10),
    onenote_access_token text,
    onenote_refresh_token text,
    onenote_expires_at timestamptz,
    onenote_auth_code text,
    onenote_notebook_id varchar(255),
    onenote_section_id varchar(255),
    use_manual_pages boolean DEFAULT FALSE,
    max_pages_per_day integer DEFAULT 2,
    is_paused boolean DEFAULT FALSE,
    reminder_time varchar(10) DEFAULT '09:00',
    last_activity_date timestamptz DEFAULT NOW(),
    timezone varchar(50) NULL,
    last_cron_processed_at timestamptz NULL,
    created_at timestamptz DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS page_references (
    page_id varchar(255) NOT NULL,
    user_id bigint NOT NULL,
    title text NOT NULL,
    source varchar(50),
    created_at timestamptz DEFAULT NOW(),
    updated_at timestamptz,
    PRIMARY KEY (page_id, user_id),
    FOREIGN KEY (user_id) REFERENCES users (telegram_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS user_progress (
    user_id bigint NOT NULL,
    page_id varchar(255) NOT NULL,
    level varchar(10),
    repetition_count integer DEFAULT 0,
    last_review_date timestamptz,
    next_review_date timestamptz,
    interval_days integer,
    success_rate integer,
    reviewed_today boolean DEFAULT FALSE,
    passed boolean DEFAULT FALSE,
    PRIMARY KEY (user_id, page_id),
    FOREIGN KEY (user_id) REFERENCES users (telegram_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS progress_history (
    user_id bigint NOT NULL,
    page_id varchar(255) NOT NULL,
    date timestamptz NOT NULL,
    score integer NOT NULL,
    mode varchar(50),
    notes text,
    PRIMARY KEY (user_id, page_id, date),
    FOREIGN KEY (user_id, page_id) REFERENCES user_progress (user_id, page_id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_next_review_date ON user_progress (user_id, next_review_date);

-- +goose Down
DROP INDEX IF EXISTS idx_next_review_date;

DROP TABLE IF EXISTS progress_history;

DROP TABLE IF EXISTS user_progress;

DROP TABLE IF EXISTS page_references;

DROP TABLE IF EXISTS users;