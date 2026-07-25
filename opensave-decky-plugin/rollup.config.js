// Decky's shared rollup preset handles the externals (react, @decky/ui,
// @decky/api) and the output shape the loader expects. Hand-rolling this is
// what left the previous config marking decky-frontend-lib external while
// declaring it as a dependency nowhere, so a clean checkout couldn't build.
import deckyPlugin from "@decky/rollup";

export default deckyPlugin();
