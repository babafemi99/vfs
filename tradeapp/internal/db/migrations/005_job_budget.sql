-- Retry budget: a job keeps retrying (with backoff) until give_up_at, then
-- goes to the dead-letter queue. max_attempts stays as a hard safety cap.
ALTER TABLE jobs ADD COLUMN give_up_at TIMESTAMPTZ NULL;
