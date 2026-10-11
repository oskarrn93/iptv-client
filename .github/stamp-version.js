// Writes the pushed tag (vX.Y.Z) into wails.json info.productVersion, which
// Wails uses for the Windows file version and the macOS bundle version. The
// committed value is a placeholder; tags are the source of truth.
const fs = require("fs");

const tag = process.env.GITHUB_REF_NAME;
// Windows file versions must be numeric, so reject pre-release suffixes early.
if (!/^v\d+\.\d+\.\d+$/.test(tag)) {
  console.error(`tag must be vX.Y.Z, got ${tag}`);
  process.exit(1);
}

const config = JSON.parse(fs.readFileSync("wails.json", "utf8"));
config.info.productVersion = tag.slice(1);
fs.writeFileSync("wails.json", JSON.stringify(config, null, 2) + "\n");
console.log(`wails.json productVersion = ${config.info.productVersion}`);
