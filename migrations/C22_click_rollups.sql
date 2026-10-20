-- C22: 15-minute rollups of click events, so dashboard totals and series
-- don't rescan every raw event. Buckets are aligned to 15 minutes in UTC.
-- Every real time zone's offset is a whole number of 15-minute steps
-- (India is +05:30, Nepal +05:45), so these buckets regroup exactly into
-- local hours and days, which hourly UTC buckets could not do for IST.

CREATE TABLE click_rollups (
    link_id       bigint      NOT NULL REFERENCES links (id),
    bucket        timestamptz NOT NULL CHECK (date_bin('15 minutes', bucket, '2000-01-01 00:00:00+00') = bucket),
    traffic_class text        NOT NULL,
    outcome       text        NOT NULL,
    source        text        NOT NULL,
    n             bigint      NOT NULL CHECK (n > 0),
    PRIMARY KEY (link_id, bucket, traffic_class, outcome, source)
);
CREATE INDEX click_rollups_bucket_idx ON click_rollups (bucket);

-- Events written before this migration. From now on the batch writer adds
-- to the rollups in the same statement that inserts the events.
INSERT INTO click_rollups (link_id, bucket, traffic_class, outcome, source, n)
SELECT link_id, date_bin('15 minutes', ts, '2000-01-01 00:00:00+00'), traffic_class, outcome, source, count(*)
FROM click_events
GROUP BY 1, 2, 3, 4, 5;
