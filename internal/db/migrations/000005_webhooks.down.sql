-- 000005_webhooks.down.sql
DROP TRIGGER IF EXISTS trg_event_touch ON webhook_events;
DROP TRIGGER IF EXISTS trg_we_touch    ON webhook_endpoints;

DROP TABLE IF EXISTS webhook_events;
DROP TABLE IF EXISTS webhook_endpoints;
