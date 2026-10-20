-- Reverses 000003_program_guide.
DROP TABLE guide_programmes;
DROP TABLE guide_channels;
ALTER TABLE playlists DROP COLUMN guide_refreshed_at;
ALTER TABLE playlists DROP COLUMN tvg_url;
ALTER TABLE playlists DROP COLUMN guide_url;
