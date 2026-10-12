// Fictional, realistic-looking playlists for the README screenshots
// (screenshots.mjs). Loaded before stub-backend.js, which calls the seed
// instead of creating its test data.
window.__stubSeed = (addPlaylist) => {
  const groups = {
    News: ['World News 24', 'Daily Report', 'Business Today', 'Metro News', 'Weather Now', 'Global Headlines'],
    Sports: ['Sports One', 'Sports Two', 'Football Central', 'Tennis World', 'Motor Racing', 'Golf Plus', 'Arena Live', 'Extreme Sports'],
    Movies: ['Cinema Classics', 'Action Movies', 'Family Films', 'Drama Premiere', 'Comedy Plus', 'Thriller Max', 'Indie Screen'],
    Kids: ['Cartoon Planet', 'Junior TV', 'Kids Club', 'Toon Time', 'Little Explorers'],
    Documentary: ['Discovery Science', 'History Vision', 'Wild Planet', 'Ocean Life', 'Space Frontier'],
    Music: ['Hits TV', 'Classic Rock', 'Jazz Lounge', 'Chart Show', 'Dance Floor'],
    Entertainment: ['Prime Entertainment', 'Reality Plus', 'Game Show Network', 'Lifestyle TV', 'Food & Travel'],
    Nordic: ['Nordic One', 'Nordic Two', 'Fjord TV', 'Aurora', 'Kanal Nord'],
  };
  const favorites = new Set(['World News 24 HD', 'Sports One HD', 'Football Central HD', 'Wild Planet HD']);
  const main = [];
  for (const [group, names] of Object.entries(groups)) {
    for (const name of names) {
      const tvgId = name.toLowerCase().replace(/\W+/g, '');
      main.push({ name: `${name} HD`, group, tvgId, favorite: favorites.has(`${name} HD`) });
      main.push({ name, group, tvgId });
    }
  }
  const regions = ['Local', 'Regional', 'International', 'Radio'];
  for (let i = 1; i <= 1200; i++) {
    const region = regions[i % regions.length];
    main.push({ name: `${region} Channel ${i}`, group: region, tvgId: `${region}${i}` });
  }
  addPlaylist('Home Provider', 'url', 'http://provider.example/…', main);
  addPlaylist('Travel Pack', 'file', 'C:\\lists\\travel.m3u', main.slice(0, 40));
};
