import { describe, it, expect } from 'vitest';
import {
  buildGroups,
  contentsLabel,
  fmtAge,
  fmtBytes,
  isEmptyResult,
  normPath,
  plannedGames,
  rootNameFor
} from './scan.js';

// Every case below is a mistake that actually reached a build, not a
// hypothetical. Until now the only way to catch any of them was to open the
// app and look.

const row = (over = {}) => ({
  id: 'x',
  name: 'Game',
  savePath: 'C:\\Games\\Game',
  role: 'only',
  groupId: 'app:1',
  measured: true,
  fileCount: 3,
  totalBytes: 300,
  latestMtime: Math.floor(Date.now() / 1000) - 86400,
  truncated: false,
  ...over
});

describe('buildGroups', () => {
  // The bug: the API returns rows in scanner order, so the save folder could
  // sort BELOW a folder that is only part of the save. Everything downstream
  // takes the first entry as the folder to track — so TrackMania would have
  // been tracked with Profiles as its save and Tracks as a location.
  it('puts the folder to track first, whatever order the daemon sent', () => {
    const groups = buildGroups([
      row({ id: 'profiles', role: 'location', savePath: 'C:\\TM\\Profiles' }),
      row({ id: 'scores', role: 'location', savePath: 'C:\\TM\\Scores' }),
      row({ id: 'tracks', role: 'primary', savePath: 'C:\\TM\\Tracks' })
    ]);

    expect(groups).toHaveLength(1);
    expect(groups[0].primary.id).toBe('tracks');
    expect(groups[0].members[0].id).toBe('tracks');
    expect(groups[0].suggested[0].id).toBe('tracks');
  });

  it('offers the save folder and its sibling pieces as one game', () => {
    const [g] = buildGroups([
      row({ id: 'tracks', role: 'primary' }),
      row({ id: 'scores', role: 'location' }),
      row({ id: 'old', role: 'alternative' }),
      row({ id: 'nested', role: 'inside' })
    ]);
    expect(g.suggested.map((m) => m.id)).toEqual(['tracks', 'scores']);
  });

  // The bug: nothing excluded a folder already tracked as its own game, so
  // "Track all N" could adopt it as a location of another game — one folder
  // owned twice, watched twice, syncing against itself.
  it('never adopts a folder that is already its own game', () => {
    const tracked = new Set([normPath('C:\\TM\\Scores')]);
    const [g] = buildGroups(
      [
        row({ id: 'tracks', role: 'primary', savePath: 'C:\\TM\\Tracks' }),
        row({ id: 'scores', role: 'location', savePath: 'C:\\TM\\Scores' }),
        row({ id: 'profiles', role: 'location', savePath: 'C:\\TM\\Profiles' })
      ],
      tracked
    );

    expect(g.suggested.map((m) => m.id)).toEqual(['tracks', 'profiles']);
    // Still listed, so it is visible rather than silently dropped.
    expect(g.members.map((m) => m.id)).toContain('scores');
  });

  it('matches tracked paths regardless of case or a trailing slash', () => {
    const tracked = new Set([normPath('c:\\tm\\scores\\')]);
    const [g] = buildGroups(
      [
        row({ id: 'tracks', role: 'primary', savePath: 'C:\\TM\\Tracks' }),
        row({ id: 'scores', role: 'location', savePath: 'C:\\TM\\Scores' })
      ],
      tracked
    );
    expect(g.suggested.map((m) => m.id)).toEqual(['tracks']);
  });

  it('gives a row with no group id a group of its own', () => {
    const groups = buildGroups([
      row({ id: 'a', groupId: '', savePath: 'C:\\A' }),
      row({ id: 'b', groupId: '', savePath: 'C:\\B' })
    ]);
    expect(groups).toHaveLength(2);
  });
});

describe('isEmptyResult', () => {
  it('is true only for a folder the daemon looked inside and found nothing in', () => {
    expect(isEmptyResult({ measured: true, fileCount: 0 })).toBe(true);
    expect(isEmptyResult({ measured: true, fileCount: 2 })).toBe(false);
  });

  // The one that matters: a folder that could not be READ also reports zero
  // files. Empty is what gets hidden, so treating unknown as empty would take
  // a real save off the list.
  it('is false when the folder could not be measured', () => {
    expect(isEmptyResult({ measured: false, fileCount: 0 })).toBe(false);
  });
});

describe('contentsLabel', () => {
  const now = Date.now();

  it('separates "empty" from "could not look"', () => {
    expect(contentsLabel({ measured: true, fileCount: 0 }, now)).toMatch(/empty/);
    expect(contentsLabel({ measured: false, fileCount: 0 }, now)).toBe('size unknown');
    expect(contentsLabel({ measured: false, fileCount: 0 }, now)).not.toMatch(/empty/);
  });

  // The bug: this read "20000 files+", as though it were twenty thousand
  // files and a bit. The count is a floor — the walk stopped at a cap.
  it('puts the truncation "+" on the number, not after the noun', () => {
    const label = contentsLabel(
      { measured: true, fileCount: 20000, totalBytes: 1024, latestMtime: now / 1000, truncated: true },
      now
    );
    expect(label).toContain('20000+ files');
    expect(label).not.toContain('files+');
  });

  it('says "1 file", not "1 files"', () => {
    const label = contentsLabel(
      { measured: true, fileCount: 1, totalBytes: 10, latestMtime: now / 1000 },
      now
    );
    expect(label).toContain('1 file ');
    expect(label).not.toContain('1 files');
  });
});

describe('fmtAge', () => {
  const now = Date.now();
  const ago = (secs) => now / 1000 - secs;

  it('reads as an age, not a date', () => {
    expect(fmtAge(0, now)).toBe('never');
    expect(fmtAge(ago(30), now)).toBe('just now');
    expect(fmtAge(ago(20 * 60), now)).toBe('20m ago');
    expect(fmtAge(ago(5 * 3600), now)).toBe('5h ago');
    expect(fmtAge(ago(22 * 86400), now)).toBe('22d ago');
    expect(fmtAge(ago(400 * 86400), now)).toBe('over a year ago');
    expect(fmtAge(ago(3 * 365 * 86400), now)).toBe('3 years ago');
  });
});

describe('fmtBytes', () => {
  it('scales', () => {
    expect(fmtBytes(0)).toBe('0 B');
    expect(fmtBytes(512)).toBe('512 B');
    expect(fmtBytes(20889)).toBe('20.4 KB');
    expect(fmtBytes(11885685)).toBe('11.3 MB');
  });
});

describe('rootNameFor', () => {
  // The name is what two devices match a location on, so it has to be
  // something the other machine derives independently — the folder's name.
  it('uses the folder name, on either separator', () => {
    expect(rootNameFor('C:\\Users\\me\\Documents\\TrackMania\\Scores')).toBe('Scores');
    expect(rootNameFor('/home/deck/.local/share/TrackMania/Scores')).toBe('Scores');
    expect(rootNameFor('C:\\TM\\Scores\\')).toBe('Scores');
  });

  it('disambiguates a repeated folder name instead of colliding', () => {
    const taken = new Set();
    expect(rootNameFor('C:\\A\\config', taken)).toBe('config');
    expect(rootNameFor('C:\\B\\config', taken)).toBe('config 2');
    expect(rootNameFor('C:\\C\\config', taken)).toBe('config 3');
  });
});

describe('plannedGames', () => {
  // Tracking a split save as separate games gives you library entries that
  // each sync a third of it.
  it('folds a game selected across several folders into one game', () => {
    const plan = plannedGames([
      row({ id: 'scores', role: 'location', groupId: 'app:7' }),
      row({ id: 'tracks', role: 'primary', groupId: 'app:7' }),
      row({ id: 'other', role: 'only', groupId: 'app:9' })
    ]);

    expect(plan).toHaveLength(2);
    const tm = plan.find((p) => p.primary.groupId === 'app:7');
    expect(tm.primary.id).toBe('tracks');
    expect(tm.extras.map((e) => e.id)).toEqual(['scores']);
  });

  it('falls back to the first row when a group has no primary', () => {
    const plan = plannedGames([row({ id: 'a', role: 'alternative', groupId: 'app:1' })]);
    expect(plan[0].primary.id).toBe('a');
    expect(plan[0].extras).toEqual([]);
  });
});
