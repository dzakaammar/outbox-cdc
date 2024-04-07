CREATE TABLE
    IF NOT EXISTS users (
        id SERIAL PRIMARY KEY,
        name TEXT NOT NULL,
        email TEXT NOT NULL,
        address TEXT NOT NULL,
        phone TEXT NOT NULL
    );

CREATE TABLE
    IF NOT EXISTS outbox (
        id SERIAL PRIMARY KEY,
        event_name TEXT NOT NULL,
        object_name TEXT NOT NULL,
        object_id TEXT NOT NULL,
        data JSONB NOT NULL,
        created_at TIMESTAMP NOT NULL DEFAULT NOW ()
    );

-- create user pglogrepl
-- with
--     replication password 'secret';
-- grant usage on schema public to pglogrepl;