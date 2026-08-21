import { describe, it, expect } from 'vitest';
import {
  addExclusion,
  addNegation,
  anchoredPattern,
  removeDirectExclusion
} from './ignorerules.js';

describe('addExclusion', () => {
  // Anchored, because ticking one row means that one file. A bare
  // "Config.gs" would also catch a Config.gs three folders down, which is
  // more than was asked for.
  it('anchors the pattern to the file that was ticked', () => {
    expect(addExclusion('', 'Config.gs')).toBe('/Config.gs');
    expect(addExclusion('', 'logs/debug.log')).toBe('/logs/debug.log');
  });

  it('keeps existing rules and appends', () => {
    expect(addExclusion('*.log', 'Config.gs')).toBe('*.log\n/Config.gs');
  });

  it('does not add the same rule twice', () => {
    expect(addExclusion('/Config.gs', 'Config.gs')).toBe('/Config.gs');
    // Also recognises the unanchored form someone may have typed themselves.
    expect(addExclusion('Config.gs', 'Config.gs')).toBe('Config.gs');
  });

  it('drops blank lines rather than accumulating them', () => {
    expect(addExclusion('*.log\n\n\n', 'Config.gs')).toBe('*.log\n/Config.gs');
  });
});

describe('removeDirectExclusion', () => {
  it('removes the rule naming the file, anchored or not', () => {
    expect(removeDirectExclusion('/Config.gs', 'Config.gs')).toBe('');
    expect(removeDirectExclusion('Config.gs', 'Config.gs')).toBe('');
  });

  it('leaves every other rule alone', () => {
    expect(removeDirectExclusion('*.log\n/Config.gs\nlogs/', 'Config.gs')).toBe('*.log\nlogs/');
  });

  // A wildcard is not something to delete on the user's behalf: dropping
  // "*.log" to keep one log would stop excluding all the others.
  it('does not touch a wildcard that happens to catch the file', () => {
    expect(removeDirectExclusion('*.log', 'logs/debug.log')).toBe('*.log');
  });

  it('clears a stale negation for the same file', () => {
    expect(removeDirectExclusion('*.log\n!/logs/keep.log', 'logs/keep.log')).toBe('*.log');
  });
});

describe('addNegation', () => {
  // The only way to rescue one file from a broader rule without abandoning
  // the rule.
  it('adds an exception under the rule that is catching it', () => {
    expect(addNegation('*.log', 'logs/keep.log')).toBe('*.log\n!/logs/keep.log');
  });

  it('is idempotent', () => {
    const once = addNegation('*.log', 'logs/keep.log');
    expect(addNegation(once, 'logs/keep.log')).toBe(once);
  });

  it('does not leave a leading blank line when the list was empty', () => {
    expect(addNegation('', 'Config.gs')).toBe('!/Config.gs');
  });
});

describe('the untick round trip', () => {
  // What the app actually does: drop any direct rule, ask the daemon whether
  // the file still cannot sync, and only then add an exception. Reproduced
  // here with the daemon's answer stubbed, since the verdict itself is never
  // computed in the client.
  const untick = (text, relPath, stillExcludedAfter) => {
    const cleaned = removeDirectExclusion(text, relPath);
    return stillExcludedAfter(cleaned) ? addNegation(cleaned, relPath) : cleaned;
  };

  it('just removes the line when that was the only thing excluding it', () => {
    expect(untick('/Config.gs', 'Config.gs', () => false)).toBe('');
  });

  it('adds an exception when a wildcard is still catching it', () => {
    expect(untick('*.log', 'logs/keep.log', () => true)).toBe('*.log\n!/logs/keep.log');
  });

  it('leaves the rest of the list working', () => {
    const out = untick('*.log\nlogs/\n/Config.gs', 'Config.gs', () => false);
    expect(out).toBe('*.log\nlogs/');
  });
});

describe('anchoredPattern', () => {
  it('is what the other helpers agree on', () => {
    expect(anchoredPattern('a/b.txt')).toBe('/a/b.txt');
  });
});
