// Editing a game's "files that shouldn't sync" list.
//
// The verdicts themselves are NEVER computed here — they come from the daemon,
// which uses the same matcher the sync engine uses. A second, nearly-right
// copy of that matching in the client would be wrong in the worst direction:
// telling someone a file is protected when it is not.
//
// What lives here is only the text editing: turning a tick into a pattern and
// an untick back out again.

/**
 * The pattern that names one file and nothing else.
 *
 * Anchored with a leading slash so it is pinned to the top of the save
 * location. A bare `Config.gs` matches a file of that name at any depth, which
 * is more than was asked for when someone ticks one row.
 */
export const anchoredPattern = (relPath) => '/' + relPath;

export const ruleLines = (text) => (text ?? '').split('\n');

/**
 * Add the rule that excludes one file, if it is not already named.
 * Returns the new text.
 */
export function addExclusion(text, relPath) {
  const anchored = anchoredPattern(relPath);
  const lines = ruleLines(text).filter((l) => l.trim() !== '');
  if (!lines.some((l) => l.trim() === anchored || l.trim() === relPath)) {
    lines.push(anchored);
  }
  return lines.join('\n');
}

/**
 * Drop every line that names this file outright, including a stale `!`
 * exception for it. Returns the new text.
 *
 * This alone may not be enough — a wildcard or folder rule can still be
 * catching the file — which is why the caller asks the daemon afterwards and
 * calls addNegation when it is still excluded. That question cannot be
 * answered here without reimplementing the matcher.
 */
export function removeDirectExclusion(text, relPath) {
  const anchored = anchoredPattern(relPath);
  const lines = ruleLines(text).filter((l) => {
    const t = l.trim();
    return t !== anchored && t !== relPath && t !== '!' + anchored && t !== '!' + relPath;
  });
  return lines.join('\n').replace(/\n{3,}/g, '\n\n');
}

/**
 * Rescue one file from a broader rule.
 *
 * The `!` exception is the only thing that can do this without abandoning the
 * wildcard — deleting `*.log` to keep one log would stop excluding all the
 * others, which is not what was asked.
 */
export function addNegation(text, relPath) {
  const anchored = anchoredPattern(relPath);
  const trimmed = (text ?? '').trimEnd();
  if (ruleLines(trimmed).some((l) => l.trim() === '!' + anchored)) return trimmed;
  return (trimmed + '\n!' + anchored).replace(/^\n+/, '');
}
