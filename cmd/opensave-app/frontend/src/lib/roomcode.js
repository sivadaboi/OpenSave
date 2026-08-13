// Room code generation.
//
// The code is the only thing standing between a stranger and your devices'
// presence: anyone who knows it joins the room, learns each device's name,
// type and tracked games, and can send pairing requests. It cannot pull a save
// without approval on the device, but that is one control, not two.
//
// The previous generator drew two words from a list of eight and a four-digit
// number: 8 x 8 x 9000 = 576,000 codes, about 19 bits. Small enough to
// enumerate the entire keyspace, which turns "guess someone's room" into
// "sweep every room there is". It also used Math.random(), which is not a
// cryptographic generator and whose internal state can be recovered from its
// own output.

// A 32-character alphabet with the shapes people mistype removed: no i, l, o
// or u, so nothing is confusable with 1 or 0 when read off a screen or over a
// call.
//
// Exactly 32 characters matters beyond readability: 256 is a whole multiple of
// 32, so taking a random byte modulo 32 is perfectly uniform. At any other
// size the low values would come up slightly more often and the code would be
// quietly weaker than its length suggests.
export const ROOM_CODE_ALPHABET = '0123456789abcdefghjkmnpqrstvwxyz';

const GROUPS = 3;
const GROUP_LEN = 4;

/**
 * A room code with about 60 bits of entropy: 12 characters from a 32-symbol
 * alphabet, grouped for transcription. Formatted as xxxx-xxxx-xxxx.
 */
export function generateRoomCode(randomBytes = defaultRandomBytes) {
	const n = GROUPS * GROUP_LEN;
	const bytes = randomBytes(n);
	if (!bytes || bytes.length < n) {
		throw new Error('room code generation got too few random bytes');
	}
	let out = '';
	for (let i = 0; i < n; i++) {
		if (i > 0 && i % GROUP_LEN === 0) out += '-';
		out += ROOM_CODE_ALPHABET[bytes[i] % ROOM_CODE_ALPHABET.length];
	}
	return out;
}

/** Entropy in bits, so a test can assert the strength rather than the format. */
export function roomCodeEntropyBits() {
	return GROUPS * GROUP_LEN * Math.log2(ROOM_CODE_ALPHABET.length);
}

// Deliberately no Math.random() fallback. If a browser had no Web Crypto the
// right answer is to fail loudly, not to hand back a code that looks the same
// and is guessable — the caller would have no way to tell the difference.
function defaultRandomBytes(n) {
	const c = globalThis.crypto;
	if (!c || typeof c.getRandomValues !== 'function') {
		throw new Error('no secure random source available for the room code');
	}
	return c.getRandomValues(new Uint8Array(n));
}
