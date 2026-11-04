-- The request that queued a job, so its logs can be followed from the HTTP
-- request into the worker. NULL for jobs queued by the worker itself.
ALTER TABLE jobs ADD COLUMN request_id TEXT NULL;
