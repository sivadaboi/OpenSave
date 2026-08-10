// Pure logic behind the auto-scan screen.
//
// This lives outside the component because it is where the mistakes were. The
// scan grid's real bugs have all been decisions, not rendering: which folder
// of a game counts as the save, whether an already-tracked folder may be
// adopted as a location of another game, whether a listing that ended on the
// cap was actually truncated. Every one of those is a function of its inputs
// and nothing else — and while it sat inside a .svelte file the only way to
// exercise it was to click the app.
//
// Nothing here touches the DOM, the API, or Svelte. Anything that needs those
// stays in the component.

/**
 * Compare save paths the way the daemon does: case-insensitively, ignoring a
 * trailing separator. A tracked path and a scan result differ by a trailing
 * slash often enough that comparing them raw silently loses the match.
 */
export const normPath = (p) => (p ?? '').replace(/[\\/]+$/, '').toLowerCase();

/**
 * A folder the daemon looked inside and found no files in.
 *
 * The `measured` half is load-bearing and must never be dropped: a folder the
 * daemon could NOT read also reports zero files, and treating that as empty
 * would hide a real save from the listing. Unknown is not empty.
 */
export const isEmptyResult = (r) => !!r.measured && r.fileCount === 0;

// Order within a game: the folder to track, then the ones offered with it,
// then the ones merely offered, then the ones already covered.
const ROLE_RANK = { primary: 0, only: 0, location: 1, alternative: 2, inside: 3 };

/**
 * Group scan results into one entry per game.
 *
 * @param rows    scan results, as the daemon returned them
 * @param tracked Set of already-tracked save paths, normalised via normPath
 */
export function buildGroups(rows, tracked = new Set()) {
  const byID = new Map();
  for (const r of rows) {
    const id = r.groupId || `id:${r.id}`;
    if (!byID.has(id)) byID.set(id, []);
    byID.get(id).push(r);
  }

  // The API returns rows in scanner order — the order the detection passes
  // ran in — so without this the save folder can sit below a folder that is
  // only part of the save, and everything downstream that says "the first
  // one" means the wrong one.
  for (const members of byID.values()) {
    members.sort((a, b) => (ROLE_RANK[a.role] ?? 9) - (ROLE_RANK[b.role] ?? 9));
  }

  return [...byID.entries()].map(([id, members]) => {
    const primary = members.find((m) => m.role === 'primary' || m.role === 'only') ?? members[0];
    return {
      id,
      primary,
      members,
      extras: members.filter((m) => m !== primary),
      // Folders offered alongside the primary as one game. "inside" and
      // "alternative" are deliberately excluded — the first cannot be tracked
      // separately at all, the second is usually a dead install.
      //
      // A folder already tracked as its own game is excluded too: adopting it
      // as a location of another game would leave one folder owned twice,
      // watched twice, and syncing against itself.
      suggested: members.filter(
        (m) => m === primary || (m.role === 'location' && !tracked.has(normPath(m.savePath)))
      )
    };
  });
}

/** Human file size. */
export function fmtBytes(n) {
  if (!n) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let i = 0;
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024;
    i++;
  }
  return `${i === 0 ? n : n.toFixed(1)} ${units[i]}`;
}

/**
 * Rough age. Rough on purpose: the question it answers is "is this folder
 * still in use", and a date is something you have to do arithmetic on.
 */
export function fmtAge(unix, now = Date.now()) {
  if (!unix) return 'never';
  const secs = now / 1000 - unix;
  if (secs < 60) return 'just now';
  if (secs < 3600) return `${Math.floor(secs / 60)}m ago`;
  if (secs < 86400) return `${Math.floor(secs / 3600)}h ago`;
  if (secs < 86400 * 365) return `${Math.floor(secs / 86400)}d ago`;
  const years = secs / 86400 / 365;
  return years < 2 ? 'over a year ago' : `${Math.round(years)} years ago`;
}

/** What a scan tile says about its folder. */
export function contentsLabel(r, now = Date.now()) {
  if (!r.measured) return 'size unknown';
  if (r.fileCount === 0) return 'empty — nothing saved here yet';
  // The "+" belongs on the number, not after the noun: counting stopped at a
  // cap, so it is a floor, not "files and a bit".
  const files = `${r.fileCount}${r.truncated ? '+' : ''} file${r.fileCount === 1 ? '' : 's'}`;
  return `${files} · ${fmtBytes(r.totalBytes)} · ${fmtAge(r.latestMtime, now)}`;
}

/**
 * A name for an extra save location, derived from its folder.
 *
 * The name is what two devices match on, so it has to be something the other
 * machine will arrive at independently — the folder's own name, never a
 * number. `taken` is mutated to reserve the name.
 */
export function rootNameFor(path, taken = new Set()) {
  const base = (path ?? '').replace(/[\\/]+$/, '').split(/[\\/]/).pop() || 'location';
  let name = base;
  for (let n = 2; taken.has(name.toLowerCase()); n++) name = `${base} ${n}`;
  taken.add(name.toLowerCase());
  return name;
}

/**
 * Split a selection into the games it will create.
 *
 * Folders of one game go in as one game with locations, not as several games
 * that happen to share a name — tracking TrackMania's Scores, Tracks and
 * Profiles separately gives three library entries that each sync a third of a
 * save.
 */
export function plannedGames(selectedRows) {
  const byGroup = new Map();
  for (const it of selectedRows) {
    const gid = it.groupId || `id:${it.id}`;
    if (!byGroup.has(gid)) byGroup.set(gid, []);
    byGroup.get(gid).push(it);
  }
  return [...byGroup.values()].map((members) => {
    const primary = members.find((m) => m.role === 'primary' || m.role === 'only') ?? members[0];
    return { primary, extras: members.filter((m) => m !== primary) };
  });
}
