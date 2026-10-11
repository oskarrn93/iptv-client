CREATE TRIGGER channels_ad AFTER DELETE ON channels BEGIN
	INSERT INTO channels_fts(channels_fts, rowid, name, group_title) VALUES ('delete', old.id, old.name, old.group_title);
END;
