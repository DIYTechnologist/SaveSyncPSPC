# RE4 (2023) — Deep Technical Reference

This is the detailed structure/process reference for RE4's support in
`internal/reengine` and `internal/engine/reengine`. `docs/dev-res2.md` is
the equivalent reference for RE2 - read that first for the shared DSSS
container/RSZ background this document doesn't repeat.

Status: **PS5 key found and confirmed against a real save. Full format-level
PC↔PS5 round trip confirmed against two real Steam saves** (checksums
valid, RSZ re-parses, correct slot IDs). **Not yet tested in-game.**
`RE4PlatformClass`'s field mapping was found from a single real sample
per platform, unlike RE2/RE3's multi-sample confirmation.

## 1. Two completely different ciphers, same title

RE4's PS5 build uses the exact same plain-Blowfish DSSS shape as RE2
(`flags=0x1`, `blowfish_option=3`, body at `0x18`, no ID field) - just
with its own key, `KeyRE4`. Its **Steam build does not use Blowfish at
all**. This was the first wrong assumption to fall: the community
reference (`kvasszn/ree-save-editor`) has no RE4 Blowfish key because
none exists - RE4 on Steam uses a cipher the source calls "Lime"
(`flags=0x10`, the same bit RE9/Requiem's "Mandarin" cipher uses - "for
RE4, it uses LIME instead of MANDARIN, frick you capcom" per that
project's own comment).

## 2. Finding `KeyRE4`: a known-plaintext oracle, not disassembly

Every DSSS container's `DSSSDSSS` check block is known plaintext once
decrypted. That makes *any* candidate key instantly and unambiguously
testable, without understanding a single line of the game's code:
decrypt the check block with the candidate, compare to `"DSSSDSSS"`.

Given a real PS5 `eboot.bin` (336 MB), every maximal run of printable,
non-space ASCII (4-64 bytes) was extracted (~2.2 million candidates) and
tested against the real save's check block in parallel. Total time: under
10 seconds. The match:

```
wa9Ui_tFKa_6E_D5gVChjM69xMKDX8QxEykYKhzb4cRNLknpCZUra   (53 bytes)
```

Confirmed properly, not just against the 8-byte check block: full
`Decode`, RSZ parse (3 objects), and the trailing cleartext bytes read
`00 00 00 00` - slot 0, matching the source container's own name
(`SAVESERVICE-LINE-0-0`).

This method - sweep candidates from the binary against a known-plaintext
oracle - is far cheaper and more reliable than disassembling to find the
key-schedule call site, which was tried first (locating the Blowfish
P-array constants in rodata, then searching for code that referenced that
address) and led nowhere: the constants are present, but nothing
referenced them in a way static analysis could follow, likely because
they belong to an unrelated/dead code path in a bundled crypto library.

## 3. The Lime container format

Real bytes, from a genuine Steam save (`data000.bin`, decrypted correctly
- see §5):

```
offset 0x00  "DSSS"          magic
offset 0x04  u32 = 2         version
offset 0x08  u32 = 0x10      flags (Mandarin/Lime bit)
offset 0x0C  [4 bytes]       alignment padding to the next 16-byte
                             boundary - observed non-deterministic
                             (garbage in one real save, zero in another);
                             the reader only seeks past it, never reads it
offset 0x10  [N blocks]      Lime blocks, 0x1220 bytes each (see §4)
             [0x80 bytes]    unused trailing data ("pretty sure its just
                             an RSA mac" - kvasszn; never read back)
             [8 bytes]       decrypted_len, u64 LE - the real byte count
                             once truncated (blocks are always a whole
                             0x1000-byte multiple, so the last block is
                             usually padded)
             [4 bytes]       murmur3_32(everything before this,
                             seed=0xffffffff), LE - same trailer as every
                             other DSSS shape
```

`N = ceil(decrypted_len / 0x1000)`. This layout was reverse-engineered
from `kvasszn/ree-save-editor`'s `save/mod.rs` (the actual `load()`
function, not just the crypto module) - critically, **`decrypted_len` is
read from the *end* of the file** (`cursor.seek(SeekFrom::End(-12))`),
not stored near the header the way a first guess (mirroring RE2's
`blowfish_option` position) would assume. Getting this wrong produced a
plausible-looking but wrong block count and a cascade of confusing
partial failures before the real position was found.

## 4. Per-block structure and the ElGamal-style scheme

Each `0x1220`-byte block:

```
[512 bytes]   enc_key: 4 pairs of (64-byte, 64-byte) big integers
[4096 bytes]  AES-128-OFB-encrypted data
[32 bytes]    SHA3-256(plaintext data) - checked, not just carried
```

The 4 pairs decrypt to 4×8 = 32 bytes: AES key (16) + IV (16).

The "encryption" is a lightweight ElGamal-style scheme over a fixed
256-bit prime group - public constants, baked into the game binary, not
secret:

```go
P = f33b6fb972a0b72515e45c391829e182ad8a9bdc0a64d3444d79c810ab863717  (little-endian)
Q = f99db75c39d0db920a72ae1c8c9470c156c54d6e05b269a2a63c648855c39b0b
R = e66f544afcce68c5ef07b9a07b277585344a1db61376e831f73b9fbd5f44f715
e = 0x14  (fixed public exponent, every pair ever encrypted uses it)
```

**The "private key" is not a secret to discover - it's a public
formula**: `u = NOT(steamID & 0xffffffff) mod Q`. Bitwise-complement the
low 32 bits of your own Steam account ID, reduce mod Q. Anyone can
compute their own `u`; this scheme binds a save to an account, it isn't
confidentiality-grade cryptography. Getting the *exact* derivation right
took two tries: the first attempt (`NOT` of the raw 64-bit Steam ID)
failed with `k` overflowing 8 bytes on decrypt, which traced back to
`save/mod.rs`'s actual call site - `let key = options.game.get_key_from_steamid(id)`
first masks to the low 32 bits (`RE4`'s table entry: `calc: |id: u64| id & 0xffffffff`),
*then* `Lime::decrypt`'s `Elgamal::init(!key)` inverts that already-masked
value - not the raw ID.

Per-pair decrypt: given ciphertext `(c0, c1)`, compute `x = c0^u mod P`,
then `k = c1 / x` - **exact integer division, not modular**. This works
because `c1` was formed as `x1 * plaintext` with no modular reduction at
encryption time, and `x` (derived via the account exponent on the
encrypting side) is algebraically identical to `x1` by commutativity of
exponentiation: `(r^e)^u ≡ (r^u)^e (mod P)`. `c0` itself is `r^e mod P` -
independent of both account and plaintext, so it's *literally the same
64 bytes in every single pair, in every block, in every save, for every
account* - confirmed directly: the first 8 bytes of the first pair were
byte-identical between two completely unrelated real saves before this
was understood, which is what first pointed at "this is a fixed
constant," not per-save content.

## 5. Getting the RSZ alignment base right

The reconstructed/truncated Lime-decrypted body isn't simply "the body,
offset 0" - it needed the same file-offset-relative alignment rule
RE2/RE3's investigations already established (see `docs/dev-res2.md` §3),
just with an offset specific to this cipher.

Three candidates were tried against a real decrypted body before finding
the right one: `0x0` (fails - desyncs deep into the object tree), `0x18`
(RE2's PS5 offset - panics with a divide-by-zero from garbage array
lengths), `0x20` (RE2's PC offset - also fails). The answer, `0x10`, came
from reading `save/mod.rs` more carefully: the game decrypts Lime blocks
**in place**, overwriting the original file buffer starting at the same
offset the encrypted region began (`data[..decrypted.len()].copy_from_slice(&decrypted)`
where `data` is a sub-slice starting at that offset) - so RSZ alignment,
which is keyed to *file* position, uses that position: `0x10`, exactly
where the 16-byte-aligned header ends and the first Lime block starts.

Confirmed: both real saves parse cleanly at `0x10` (3 objects each,
matching the PS5-side object count exactly).

## 6. Building it in Go

Nothing exotic: `math/big` for the modular exponentiation and exact
division (no elliptic-curve point math needed - despite "Elgamal" in the
community source's naming, this is discrete-log-over-a-prime-field, not
EC), `crypto/aes` + `cipher.NewOFB` for the per-block cipher,
`golang.org/x/crypto/sha3` for the checksums (already available at the
project's pinned `golang.org/x/crypto` version, needed adding to
`go.sum` via `go get golang.org/x/crypto/sha3`). `internal/reengine/lime.go`
implements `LimeDecode`/`LimeEncode`/`LimeHeaderValid`; `internal/reengine/re4.go`
implements `ConvertRE4PCToPS5`/`ConvertRE4PS5ToPC` (RE4 needs its own
converter rather than the shared `TitleConfig` machinery RE2/RE3 use,
since its two sides are genuinely different cipher families, not just
different flags within the same one).

Validated directly against the two real Steam saves at every stage:
per-block SHA3-256 checksums all verify, full `LimeEncode`/`LimeDecode`
round trip is byte-identical, a wrong Steam ID is correctly rejected, and
the complete `ConvertRE4PCToPS5` → `ConvertRE4PS5ToPC` round trip
produces valid, re-parseable output on both ends.

## 7. The platform-identity field - a caveat

Diffing the real (now-decryptable) Steam save against the real PS5 save
found the same two field hashes RE2/RE3 use (`0xb41fa365` enum,
`0xe231b945` bool) inside class `0x100e60`. But unlike RE2/RE3's clean
binary split:

| Field | PC (1 sample) | PS5 (1 sample) |
|---|---|---|
| `0xb41fa365` (Enum) | 5 | 2 |
| `0xe231b945` (Boolean) | false | false |

The enum values differ cleanly (5 vs 2) - plausibly a wider platform
enum than RE2/RE3's PC=3/PS5=2, since RE4 ships on more platforms
simultaneously (Xbox variants, PS4/Xbox One as well as current-gen). The
boolean is `false` on **both** sides in this one sample, unlike RE2/RE3
where it cleanly split PC=true/PS5=false - it may not be
platform-discriminating for RE4 at all, or this single sample simply
doesn't distinguish it. This is genuinely less certain than RE2/RE3's
mapping (checked against 3-4 samples per side there); treat
`RE4PlatformClass`'s exact values as a working hypothesis until confirmed
either by more samples or an actual in-game load test.

## 8. CLI wiring: the Steam ID problem

Every other engine's `ConvertToPS5`/`ConvertFromPS5` can decrypt a PC
save with a fixed, config-known key. RE4 can't - `LimeDecode` needs the
account's Steam ID as an actual runtime input, not a profile constant.
This is the first engine in this project needing a value that isn't
knowable ahead of time and isn't discoverable from Garlic (the way a PS5
save slot name is, via `--ps5-save-name`).

Rather than change the `engine.Engine` interface (which would ripple
through all four engines for a fact only one currently needs), the value
is threaded the same way `--ps5-save-name` already is: a new
`gameapi.SaveImage.SteamID` field, populated by `bridge.resolveDynamicImages`
from a new `bridge.Options.SteamID` (in turn from a new global `--steam-id`
CLI flag), before `ConvertToPS5`/`ConvertFromPS5` run. `internal/engine/reengine`
special-cases `cfg.Title == "re4"` to route through `convertRE4ToPS5`/`convertRE4FromPS5`
instead of the shared `TitleConfig` path, requiring `--steam-id` and
erroring clearly if it's missing rather than attempting (and failing) a
keyless decrypt. This mechanism is designed to be reusable: Requiem's
Mandarin cipher will need the same kind of account-bound input.

## 9. What's left

- Live in-game test, both directions - the format-level work is done and
  self-consistent, but nothing has touched a real console or the actual
  RE4 client yet.
- `RE4PlatformClass`'s exact field mapping (§7) would benefit from a
  second real sample per side, the way RE2/RE3's were cross-checked.
- `KeyRE7`/`KeyRE8` remain unverified (carried from the same community
  source as `KeyRE3`, which turned out correct) - if either title's PS5
  save also turns out to be the unencrypted `flags=0x0` shape (RE3's
  pattern), the same "read a real PC save, diff for the platform class"
  process documented here and in `docs/dev-res2.md` applies directly.
