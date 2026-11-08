-- Hours to add to clock times (HH:MM) in a playlist's channel and programme
-- titles, for providers that write them in another time zone.
ALTER TABLE playlists ADD COLUMN title_time_offset INTEGER NOT NULL DEFAULT 0;
