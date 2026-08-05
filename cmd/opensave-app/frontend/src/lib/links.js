// Every destination the app can send someone to outside itself, in one file.
// A link that leaves the app is one worth being able to audit at a glance
// rather than hunting for a string literal in a view.
//
// All of these are opened with native.openExternal — in the system browser,
// never inside the app window.

export const DISCORD_URL = 'https://discord.gg/hvBv92DZvn';
export const DONATE_URL = 'https://opensave.gumroad.com/l/usygu';
export const GITHUB_URL = 'https://github.com/Liquid-co/OpenSave';
