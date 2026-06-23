// Committed Markdown fixtures mirroring the REAL /wiki wire format produced by
// engine/wiki/wiki.go (SW-041). They are the contract anchor for the SW-046
// render/preservation/coverage tests so the tests can't silently drift from the
// bytes the Engine actually emits. Imported as raw strings (Vite `?raw`).
import indexMd from "./index.md?raw";
import indexEmptyMd from "./index-empty.md?raw";
import indexSingleMd from "./index-single.md?raw";
import indexLargeMd from "./index-large.md?raw";
import community1Md from "./community-1.md?raw";
import communitySingletonMd from "./community-singleton.md?raw";

export {
  indexMd,
  indexEmptyMd,
  indexSingleMd,
  indexLargeMd,
  community1Md,
  communitySingletonMd,
};
