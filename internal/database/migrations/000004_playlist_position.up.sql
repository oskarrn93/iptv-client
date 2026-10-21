-- Playlists are ordered by the user (drag and drop) rather than by name.
-- Existing playlists keep their alphabetical order.
ALTER TABLE playlists ADD COLUMN position INTEGER NOT NULL DEFAULT 0;
UPDATE playlists SET position = o.n
FROM (SELECT id, ROW_NUMBER() OVER (ORDER BY name COLLATE NOCASE, id) AS n FROM playlists) AS o
WHERE playlists.id = o.id;
