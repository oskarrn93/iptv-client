-- A disabled playlist is kept but hidden from the playlist list.
ALTER TABLE playlists ADD COLUMN disabled BOOLEAN NOT NULL DEFAULT FALSE;
