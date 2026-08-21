import { describe, it, expect, vi } from 'vitest';
import { generateRoomCode, roomCodeEntropyBits, ROOM_CODE_ALPHABET } from './roomcode.js';

describe('room code strength', () => {
	// The reason this file exists. The old generator was ~19 bits, which is
	// sweepable; anything that drags it back down should fail here loudly
	// rather than ship looking the same.
	it('has enough entropy that the keyspace cannot be swept', () => {
		expect(roomCodeEntropyBits()).toBeGreaterThanOrEqual(48);
	});

	it('is nowhere near the 19 bits it replaced', () => {
		const old = Math.log2(8 * 8 * 9000);
		expect(old).toBeLessThan(20); // documents what was wrong
		expect(roomCodeEntropyBits()).toBeGreaterThan(old * 2.5);
	});

	it('uses a 32-symbol alphabet, so byte % 32 is unbiased', () => {
		expect(ROOM_CODE_ALPHABET).toHaveLength(32);
		expect(new Set(ROOM_CODE_ALPHABET).size).toBe(32); // no duplicates
		expect(256 % ROOM_CODE_ALPHABET.length).toBe(0);
	});

	it('leaves out the characters people mistype', () => {
		for (const ch of ['i', 'l', 'o', 'u']) {
			expect(ROOM_CODE_ALPHABET).not.toContain(ch);
		}
	});
});

describe('room code format', () => {
	it('is three groups of four, dash separated', () => {
		expect(generateRoomCode()).toMatch(/^[0-9a-z]{4}-[0-9a-z]{4}-[0-9a-z]{4}$/);
	});

	it('only ever emits alphabet characters', () => {
		for (let i = 0; i < 200; i++) {
			for (const ch of generateRoomCode().replace(/-/g, '')) {
				expect(ROOM_CODE_ALPHABET).toContain(ch);
			}
		}
	});

	it('maps every possible byte value into the alphabet', () => {
		// Feed all 256 byte values; nothing may fall outside the alphabet or
		// come back undefined, which is what an off-by-one in the modulo does.
		for (let b = 0; b < 256; b++) {
			const code = generateRoomCode(() => new Uint8Array(12).fill(b));
			expect(code).toMatch(/^[0-9a-z]{4}-[0-9a-z]{4}-[0-9a-z]{4}$/);
		}
	});
});

describe('room code randomness source', () => {
	it('draws from crypto.getRandomValues', () => {
		const spy = vi.spyOn(globalThis.crypto, 'getRandomValues');
		generateRoomCode();
		expect(spy).toHaveBeenCalled();
		spy.mockRestore();
	});

	it('never falls back to Math.random', () => {
		const spy = vi.spyOn(Math, 'random');
		for (let i = 0; i < 50; i++) generateRoomCode();
		expect(spy).not.toHaveBeenCalled();
		spy.mockRestore();
	});

	it('fails loudly with no secure source rather than returning a weak code', () => {
		expect(() => generateRoomCode(() => new Uint8Array(3))).toThrow(/too few random bytes/);
	});

	it('produces distinct codes', () => {
		const seen = new Set();
		for (let i = 0; i < 500; i++) seen.add(generateRoomCode());
		expect(seen.size).toBe(500);
	});

	it('uses the whole alphabet, not a corner of it', () => {
		const seen = new Set();
		for (let i = 0; i < 2000; i++) {
			for (const ch of generateRoomCode().replace(/-/g, '')) seen.add(ch);
		}
		expect(seen.size).toBe(ROOM_CODE_ALPHABET.length);
	});
});
