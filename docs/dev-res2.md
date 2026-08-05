# RE2 — Deep Technical Reference

This is the detailed structure/process reference for `internal/reengine` and `internal/engine/reengine` (Resident Evil 2, title `PPSA04288`). `docs/ressave.md` is the narrative investigation log (what was tried, in what order, what each result meant); this document is the reference manual - byte layouts, algorithms, and the full eboot.bin disassembly methodology with actual tool commands and listings. `docs/dev.md`'s "Resident Evil 2" section is a short pointer to both.

Status: **PC → PS5 conversion confirmed working in-game** (two different real saves loaded correctly). PS5 → PC is implemented and unit-tested but not confirmed in-game. See `docs/ressave.md` for what's left.

## 1. Container format ("DSSS")

RE Engine's save container (RE2/RE3/RE7/RE8 share this shape):

```
offset 0x00  "DSSS"            magic
offset 0x04  u32 = 2           version (only 2 observed/handled)
offset 0x08  u32               flags: 0x1 Blowfish, 0x2 HasID, 0x4 Citrus, 0x8 Deflate, 0x10 Mandarin/Lime
offset 0x0C  u32               blowfish_option (3 on every real save; 0 = not encrypted)
offset 0x10  [8 bytes]         encrypted "DSSSDSSS" self-check block
offset 0x18  [8 bytes, only if HasID]  encrypted account/Steam ID, 8-byte aligned
             [N bytes]         encrypted body (RSZ field data, see §3)
             [4 bytes]         murmur3_32(everything before this, seed=0xffffffff), little-endian
```

**PC saves set Blowfish + HasID** (the ID field holds the Steam account ID). **PS5 saves set only Blowfish** - no ID field; account identity there comes from the PS5 container itself (`sce_sys/param.sfo`'s `ACCOUNT_ID`), not from inside the `.bin` payload. This one-field difference is what makes `PCDataOffset = 0x20` vs `PS5DataOffset = 0x18` (§4/§7) - the entire root cause traces back to this 8-byte difference.

The body's length is **not** required to be a multiple of 8: Blowfish-CBC covers only the 8-byte-aligned prefix, and the trailing 1-7 bytes are stored in the clear. On every real save, those trailing bytes are the save's own slot number as a little-endian `u32` (`data000.bin` → `00 00 00 00`, `data00-1.bin` → `ff ff ff ff`, `data021Slot.bin` → `15 00 00 00`). `internal/reengine/dsss.go`'s `Decode`/encode carry this remainder through explicitly - an earlier version silently truncated it (Blowfish-CBC decrypt only processes whole blocks), which was the first bug found, by a round-trip test.

## 2. Cipher: Blowfish, little-endian variant

Standard Blowfish (Schneier's 1993 cipher, public domain), CBC, all-zero IV, no padding. The only non-standard part: RE Engine reads/writes each 8-byte block's two 32-bit halves as **little-endian**, where textbook Blowfish (and Go's `golang.org/x/crypto/blowfish`) uses big-endian. `internal/reengine/blowfish.go` wraps the standard cipher by reversing each 4-byte half's byte order before/after encrypting a block.

Per-title keys (fixed constants baked into each game's binary, community-documented, not derived from account data):

```go
KeyRE2 = "K<>$cl%isqA|~nV4W5~3z_Q)j]5DHdB9sb{cI9Hn&Gqc-zO8O6zf"
KeyRE3 = "mAz{]jeQ+uxyNH*d<Dt2kC5r=3M9RV6c$TaG[b|}^%&)En4F(Wvp"
KeyRE7 = "hHGb4nS653aRT29jy"
KeyRE8 = "j1lL1AOR31sd4HKJS90fs"
```

Only `KeyRE2` has actually been exercised against real saves. Checksum: standard MurmurHash3 (x86, 32-bit), over the whole file except the trailing 4 hash bytes, seed `0xffffffff`.

## 3. RSZ field format

RE Engine's field data (everything past the container header) is a tagged-object format: each field is `(hash u32, type i32, value)`, self-describing enough to walk without an external schema. Types: Array(−1), Unknown(0), Enum(1), Boolean(2), S8..U64, F32/F64, C8/C16, String(0xf), Struct(0x10), Class(0x11).

**The critical fact: field alignment is computed against the body's absolute file offset, not body-relative.** A PC body starts at `0x20` (32, 16-aligned). A PS5 body starts at `0x18` (24, 8-aligned but not 16-aligned). Every 16-byte alignment inside the body lands 8 bytes away from a body-relative calculation whenever the two bases disagree - which they always do between PC and PS5. `internal/reengine/rsz.go`'s `rszCursor` carries a `base` field; `alignUp` computes `(c.base + c.pos) % n`, not `c.pos % n`. `ReadRSZObjects(body, dataOffset)` takes the body's real file offset explicitly for this reason.

This was found by a differential test: dumping class `0x3b9a2a09` from the PC save (which parsed cleanly, 135,748 fields) gave a known-good 9-field layout; the PS5 save's corresponding struct payloads sat at body offsets ≡ 8 (mod 16) - exactly one header's worth of skew. It surfaced initially as fields with zero hash and zero type tag, which looked like legitimate "empty" entries - and the community reference implementation's own `// TODO: Add Struct weird shit handling` comment made "the Struct encoding is unmapped" a very plausible wrong diagnosis before the real cause was found.

**This fact was discovered fixing the *parser* and, at the time, never applied to the *converter*** - `ConvertPCToPS5` kept copying bodies verbatim between containers with different bases for weeks after the alignment rule was known. That gap is the entire reason the eboot.bin disassembly investigation (§6) became necessary: every crash symptom traced back to this one already-known, un-applied fact.

`internal/reengine/rszwrite.go`'s `WriteRSZObjects(objs, dataOffset, trailer)` re-emits a parsed tree at a *different* base than it was parsed at - which is exactly what re-aligning a body for its new container means. Two subtleties made it faithful: the format's **declared size** for a value is not always its **consumed size** (e.g. a Boolean may declare 4 bytes but occupy 1, with alignment absorbing the rest - re-emitting the consumed size corrupts both the size field and everything after it); and an **empty string has declared length 0 with no NUL terminator at all** (a non-empty string round-trips by re-appending the trailer NUL the reader trims, but blindly doing that for an empty string lengthens the body).

## 4. The conversion algorithm

`internal/reengine/convert.go`'s `ConvertPCToPS5`/`ConvertPS5ToPC`:

1. `Decode` the source container, check `HashValid`.
2. Parse RSZ objects at the source's real `DataOffset`.
3. Retarget the two platform-identity fields in class `0x8b7dd7a1` (§5) - fails unless exactly 2 fields were patched (a safety check: silently patching zero or more-than-two would mean the schema assumption is wrong).
4. Probe-write at the *source* base first, purely to locate the format's own trailing bytes/trailer content correctly.
5. Re-write (`WriteRSZObjects`) at the *destination* base - this is the re-alignment step.
6. `Build` the new container (§1/§2) with the target's flags (`HasID: false` for PS5, `true` for PC).
7. Verify: re-`Decode` and re-parse the just-built output before returning it, so a subtly wrong output fails loudly here rather than silently reaching a console.

## 5. The platform-identity fields

Class `0x8b7dd7a1` (a small, fixed top-level settings object present in every save) carries two fields with a **perfectly consistent split, zero exceptions**, across every sample checked (3 PC saves, 4 PS5 saves, both characters, multiple accounts):

| Field | Type | PC (3/3) | PS5 (4/4) |
|---|---|---|---|
| `0xb41fa365` | Enum | `3` | `2` |
| `0xe231b945` | Boolean | `true` | `false` |

This is the RE2 analog of BG3's `"Platform": "Steam"/"Prospero"` field. Without retargeting these, the game parses the save successfully and then refuses it as "not compatible" - the third distinct failure mode in the progression (§8).

Despite looking like the obvious blocker, patching **only** these two fields into a body still at the *wrong* alignment base still crashed identically (§8) - the platform-field fix and the alignment fix are both independently necessary, and testing them out of order made the platform-field fix look like a dead end when the actual problem was upstream of it.

## 6. Live-device test log and the failure progression

Full step-by-step log lives in `docs/ressave.md` ("Live-device findings"). The compressed version - four required fixes, each isolated by a *different* observed failure:

| State | Result in-game |
|---|---|
| Verbatim body copy (wrong alignment base, no platform fields) | Hard crash (SIGSEGV, null write on `SaveThread`) |
| Body kept at `0x20` (ID field retained, so alignment happens to match) | "Can't load the saved data because it is corrupted" |
| Re-aligned for `0x18` (native PS5 shape), no platform fields | "This save data is not compatible and cannot be used" |
| ...plus platform fields retargeted | **Loads** |

Also ruled out along the way (all via live device tests or Garlic-level static inspection, before any disassembly): patch/build version mismatch, container/block allocation size (`SAVEDATA_BLOCKS` = 256 ≈ 8MB), character mismatch, PS5 keystone/signing, RSZ schema divergence (a full class-by-class diff across platforms found zero classes sharing a hash but differing in field layout), and `param.sfo`'s `PARAMS` blob as a content digest (pulled from three real containers on both accounts: byte-identical between two completely different files, differs by exactly 2 bytes from a third - the signature of a small save-type flag, not a hash).

A "kitchen sink" test - patching all 30 fields (33 occurrences) that showed zero value-overlap between platforms, simultaneously, in one save - **also crashed identically**. That closed out black-box (no-disassembly) investigation entirely: nothing expressible as a save-data field value difference was the cause.

## 7. Getting a real crash dump: `klogsrv`

The test PS5 is jailbroken (required for Garlic itself) and runs `klogsrv`, homebrew that streams the kernel/syslog log over a raw TCP socket on port **3232**. No special client is needed - it's plain text over TCP:

```sh
# capture to a file in the background while reproducing the crash
timeout 300 bash -c 'cat < /dev/tcp/<ps5-ip>/3232' > klog-capture.log 2>&1 &
```

This was run twice, on two very different save contents (a 39-hour save and a fresh/early save), each time re-uploading a known-crashing converted save via Garlic's `upload_file` endpoint immediately before triggering "Continue" in-game. Both captures show the identical crash:

```
#
# A user thread receives a fatal signal
#
# signal: 11 (SIGSEGV)
# thread name: SaveThread
# proc name: eboot.bin
# reason: page fault (user write data, page not present)
# fault address: 0000000000000000
#
# registers:
# rdi: 0000000000000000
# r14: 000000005507376f          <- first capture only; see §9.1
# rip: 0000000003095285          <- IDENTICAL in both captures
#
# backtrace:
# 0000000003096673
# 0000000003095942
# 0000000003095b11
# 0000000003096673
# 0000000003095942
# 0000000003095b11
...
# 0000000003094b62
# 000000000309449c
# 0000000003094315
# /app0/eboot.bin
#  xotext: 0000000000400000:0000000005e80000 nsegs: 4
```

The dump's own `dynamic libraries` section gives the module's runtime load address directly: `/app0/eboot.bin` maps text at `0x400000` (`xotext: 0000000000400000:...`). That, plus `rip`, is everything needed to map the crash back into the file (§8).

A recurring `apr_resolve_vnode() line=5970 error=30` immediately after every `SAVESERVICE-LINE-0-*` mount looked promising at first (a kernel-level VFS error right at mount time) but turned out to appear identically on saves that load fine natively too (`21Slot`, `-1`) - routine mount-check noise, not the cause. Worth naming as a documented dead end so a future investigation doesn't re-chase it.

## 8. The eboot.bin disassembly

### 8.1 Locating and validating the binary

The user supplied a copy of the PS5 title package's app directory (`PPSA04288-app/eboot.bin`, build v01.003 - matching the console's own patch level). First check: is it actually decrypted and analyzable, or still SELF-encrypted?

```sh
$ file eboot.bin
eboot.bin: data
$ od -A x -t x1z eboot.bin | head -4
000000 4f 15 3d 1d 00 01 01 12 01 01 00 00 60 05 10 05  >O.=.........`...<
$ readelf -h eboot.bin
readelf: Error: Not an ELF file - it has the wrong magic bytes at the start
```

`0x1D3D154F` is Sony's SELF (Signed ELF) magic, not a plain ELF - `readelf` can't parse the outer container directly. Fake-signed/decrypted PS5 dumps commonly carry a real, unencrypted ELF inside; a magic-byte scan over the first 128KB found it:

```python
data = open('eboot.bin','rb').read(0x20000)
idx = data.find(b'\x7fELF')
# -> found at offset 0x1a0
```

Parsing the ELF header at that offset confirmed a valid x86-64 executable:

```
class=2 (2=64bit) data=1 (1=LE)
e_type=0xfe10 (SCE dynamic exec) e_machine=0x3e (x86-64)
e_entry=0x70 e_phoff=0x40 phnum=14
```

### 8.2 Mapping the crash address to a file offset

Walking the 14 program headers (manually, via `struct.unpack_from` reads of each `Elf64_Phdr`) found the `LOAD` segment containing the crash instruction:

```
[ 0] LOAD  --X off=0x00004000 vaddr=0x00000000 filesz=0x05a7d76c memsz=0x05a7d76c   <- contains crash address
```

The segment's own `vaddr` is `0` - but the crash dump's own module list (§7) says the game's text actually loads at runtime address `0x400000`. So the mapping from a crash-dump `rip` to a file offset is:

```
module_offset = rip - 0x400000
file_offset   = 0x1a0 (ELF start) + 0x4000 (segment file offset) + module_offset
```

For the observed crash, `rip = 0x3095285`:

```
module_offset = 0x3095285 - 0x400000 = 0x2c95285
file_offset   = 0x1a0 + 0x4000 + 0x2c95285 = 0x2c99425
```

Reading 16 bytes on either side of that file offset and eyeballing them (`4881c4e80000005b415e415f5dc331ff` before, `4889c6e8b3f7ffff498b07483b45e074` after) showed a textbook function epilogue (`add rsp,0xe8` / `pop rbx` / `pop r14` / `pop r15` / `pop rbp` / `ret`) - confirming real, decrypted, coherent x86-64 code at exactly the calculated location, before investing in a proper disassembly pass.

### 8.3 Disassembly tooling

Checked what was available before choosing a tool:

```sh
$ which objdump ndisasm gdb; python3 -c "import capstone" 2>/dev/null || echo "no capstone"
/home/linuxbrew/.linuxbrew/bin/objdump
/usr/bin/gdb
no capstone
```

`capstone` (the usual choice for scripted disassembly) wasn't installed and radare2/IDA weren't used either - `objdump` was already present and sufficient. `gdb` was available but unused: this is *static* analysis of a file on disk, not live debugging of a running PS5 process (no debugger attach point exists on a retail console this way).

A small reusable shell script wrapped the offset math and extraction so any address could be disassembled on demand:

```sh
#!/bin/bash
# Disassemble N bytes at a runtime virtual address in RE2's eboot.bin.
# usage: dis.sh <vaddr-hex> [len]
EB="<path>/PPSA04288-app/eboot.bin"
ELF=0x1a0; SEGOFF=0x4000; BASE=0x400000
VA=$1; LEN=${2:-0x100}
OFF=$(python3 -c "print($ELF + $SEGOFF + ($VA - $BASE))")
TMP=$(mktemp)
dd if="$EB" bs=1 skip=$OFF count=$((LEN)) of=$TMP status=none
objdump -D -b binary -m i386:x86-64 -M intel --adjust-vma=$VA $TMP 2>/dev/null | tail -n +7
rm -f $TMP
```

`--adjust-vma` makes `objdump`'s output addresses match the real runtime addresses directly (rather than 0-based offsets into the extracted window), so disassembly listings can be read and cross-referenced against the crash dump's addresses without further arithmetic.

### 8.4 The crash site

```
0000000003095220 <.data>:
 3095220:  55                      push   rbp
 3095221:  48 89 e5                mov    rbp,rsp
 3095224:  41 57                   push   r15
 3095226:  41 56                   push   r14
 3095228:  53                      push   rbx
 3095229:  48 81 ec e8 00 00 00    sub    rsp,0xe8
 3095230:  4c 8b 3d 19 ba b8 03    mov    r15,QWORD PTR [rip+0x3b8ba19]        # 0x6c20c50
 3095237:  48 89 f0                mov    rax,rsi
 309523a:  48 85 ff                test   rdi,rdi                 ; is arg1 (the object) null?
 309523d:  49 8b 0f                mov    rcx,QWORD PTR [r15]
 3095240:  48 89 4d e0             mov    QWORD PTR [rbp-0x20],rcx
 3095244:  74 3d                   je     0x3095283                ; yes -> null branch
 3095246:  48 89 fb                mov    rbx,rdi                  ; normal path: use the object
 3095249:  8b bf b0 01 00 00       mov    edi,DWORD PTR [rdi+0x1b0]
 309524f:  4c 8d b5 00 ff ff ff    lea    r14,[rbp-0x100]
 3095256:  48 89 c2                mov    rdx,rax
 3095259:  4c 89 f6                mov    rsi,r14
 309525c:  e8 cf 00 00 00          call   0x3095330
 3095261:  48 89 df                mov    rdi,rbx
 3095264:  4c 89 f6                mov    rsi,r14
 3095267:  e8 d4 f7 ff ff          call   0x3094a40                 ; normal-path log call
 309526c:  49 8b 07                mov    rax,QWORD PTR [r15]
 309526f:  48 3b 45 e0             cmp    rax,QWORD PTR [rbp-0x20]
 3095273:  75 21                   jne    0x3095296
 3095275:  48 81 c4 e8 00 00 00    add    rsp,0xe8
 309527c:  5b                      pop    rbx
 309527d:  41 5e                   pop    r14
 309527f:  41 5f                   pop    r15
 3095281:  5d                      pop    rbp
 3095282:  c3                      ret
 3095283:  31 ff                   xor    edi,edi                   ; NULL BRANCH: rdi = 0
 3095285:  48 89 c6                mov    rsi,rax                   ; <-- rip reported in the crash dump
 3095288:  e8 b3 f7 ff ff          call   0x3094a40                 ; call with rdi = NULL
 309528d:  49 8b 07                mov    rax,QWORD PTR [r15]
 3095290:  48 3b 45 e0             cmp    rax,QWORD PTR [rbp-0x20]
 3095294:  74 df                   je     0x3095275
 3095296:  e8 05 f2 e0 02          call   0x5ea44a0
```

The crash dump's `rdi: 0000000000000000` is exactly what `xor edi,edi` (`0x3095283`, two instructions before the reported `rip`) produces - strong confirmation this specific binary corresponds to what actually crashed on the console, cracked/unlocked build or not. Function entry tests its first argument for null (`test rdi,rdi` / `je`); on the null path it still calls the same logging function at `0x3094a40`, just with `rdi = NULL` instead of a real object pointer.

### 8.5 `0x3094a40` is an assert/log routine, not a deserializer

```
0000000003094a40 <.data>:
 3094a40:  55                      push   rbp
 3094a41:  48 89 e5                mov    rbp,rsp
 3094a44:  53                      push   rbx
 3094a45:  50                      push   rax
 3094a46:  48 89 f1                mov    rcx,rsi
 3094a49:  48 85 ff                test   rdi,rdi
 3094a4c:  74 1f                   je     0x3094a6d
 3094a4e:  80 39 23                cmp    BYTE PTR [rcx],0x23        ; is msg[0] == '#' ?
 3094a51:  75 48                   jne    0x3094a9b
 3094a53:  80 79 01 20             cmp    BYTE PTR [rcx+0x1],0x20    ; is msg[1] == ' ' ?
 3094a57:  75 5e                   jne    0x3094ab7
 3094a59:  b8 01 00 00 00          mov    eax,0x1
 3094a5e:  48 8b 97 80 00 00 00    mov    rdx,QWORD PTR [rdi+0x80]
 3094a65:  48 01 c1                add    rcx,rax
 3094a68:  48 85 d2                test   rdx,rdx
 3094a6b:  75 3f                   jne    0x3094aac
 3094a6d:  48 8b 1d d4 c8 b8 03    mov    rbx,QWORD PTR [rip+0x3b8c8d4]        # 0x6c21348
 3094a74:  48 8d 35 64 71 88 03    lea    rsi,[rip+0x3887164]        # 0x691bbdf   ; format string
 3094a7b:  48 89 ca                mov    rdx,rcx
 3094a7e:  31 c0                   xor    eax,eax                   ; varargs convention: al=0 fp-args
 3094a80:  48 89 df                mov    rdi,rbx
 3094a83:  e8 58 1f e1 02          call   0x5ea69e0                 ; printf-style vararg call
```

The `cmp [rcx],0x23` / `cmp [rcx+1],0x20` pair tests for a literal `"# "` message prefix, `lea rsi,[rip+...]` loads a format-string address, and `xor eax,eax` immediately before the `call` is the standard x86-64 System V varargs convention for a printf-family call (`al` = number of vector registers used for floating-point args, zeroed when none are). This is Capcom's own assert/logging call, not save-parsing code - **the crash is the error *reporter* faulting**, in a retail build where the logger global (`[r15]` in §8.4, dereferenced again inside this function) is null. The game had already detected a problem with the save and was trying to report it; the report itself is what segfaults.

### 8.6 Tracing the recursion: the array-allocation overflow guard

The backtrace repeats `0x3096673 → 0x3095942 → 0x3095b11` - a recursive tree walk (the RSZ deserializer descending through nested classes/arrays). Disassembling around the innermost frame:

```
0000000003095ab0 <.data>:
 3095ab0:  8c 03                   mov    WORD PTR [rbx],es
 3095ab2:  e8 39 ed ff ff          call   0x30947f0
 ...
 3095ac0:  55                      push   rbp                      ; function entry
 3095ac1:  48 89 e5                mov    rbp,rsp
 3095ac4:  41 57                   push   r15
 3095ac6:  41 56                   push   r14
 3095ac8:  41 55                   push   r13
 3095aca:  41 54                   push   r12
 3095acc:  53                      push   rbx
 3095acd:  50                      push   rax
 3095ace:  85 d2                   test   edx,edx
 3095ad0:  0f 88 cd 00 00 00       js     0x3095ba3
 3095ad6:  41 89 cc                mov    r12d,ecx
 3095ad9:  85 c9                   test   ecx,ecx
 3095adb:  0f 8e c2 00 00 00       jle    0x3095ba3
 3095ae1:  4d 89 c6                mov    r14,r8
 3095ae4:  4d 85 c0                test   r8,r8
 3095ae7:  0f 84 b6 00 00 00       je     0x3095ba3
 3095aed:  89 d3                   mov    ebx,edx
 3095aef:  49 89 f7                mov    r15,rsi
 3095af2:  48 85 f6                test   rsi,rsi
 3095af5:  75 08                   jne    0x3095aff
 3095af7:  85 db                   test   ebx,ebx
 3095af9:  0f 8f a4 00 00 00       jg     0x3095ba3
 3095aff:  b8 ff ff ff 7f          mov    eax,0x7fffffff
 3095b04:  29 d8                   sub    eax,ebx
 3095b06:  44 39 e0                cmp    eax,r12d                 ; INT_MAX - count >= additional ?
 3095b09:  7c 3f                   jl     0x3095b4a                ; overflow -> fail
 3095b0b:  41 8d 04 1c             lea    eax,[r12+rbx*1]           ; newCount = count + additional
 3095b0f:  31 d2                   xor    edx,edx
 3095b11:  48 63 c8                movsxd rcx,eax                  ; <-- backtrace frame
 3095b14:  48 c7 c0 ff ff ff ff    mov    rax,0xffffffffffffffff
 3095b1b:  49 f7 f6                div    r14                      ; UINT64_MAX / elemSize
 3095b1e:  48 39 c8                cmp    rax,rcx                  ; would newCount*elemSize overflow?
 3095b21:  72 27                   jb     0x3095b4a                ; overflow -> fail
 3095b23:  49 0f af ce             imul   rcx,r14
 3095b27:  48 85 c9                test   rcx,rcx
 3095b2a:  74 1e                   je     0x3095b4a
 3095b2c:  48 85 ff                test   rdi,rdi
 3095b2f:  74 2e                   je     0x3095b5f
 3095b31:  48 8b 87 80 03 00 00    mov    rax,QWORD PTR [rdi+0x380]
 3095b38:  48 85 c0                test   rax,rax
 3095b3b:  74 22                   je     0x3095b5f
 3095b3d:  48 89 ce                mov    rsi,rcx
 3095b40:  ff d0                   call   rax                       ; the actual allocator call
 3095b42:  49 89 c5                mov    r13,rax
 3095b45:  48 85 c0                test   rax,rax
 3095b48:  75 25                   jne    0x3095b6f                 ; success
 3095b4a:  45 31 ed                xor    r13d,r13d                 ; FAILURE PATH: return NULL
```

This is an array/buffer allocator with textbook overflow guards: `newCount = count + additional` checked against `INT_MAX`, then `newCount * elementSize` checked against `UINT64_MAX` via a division trick (`UINT64_MAX / elemSize`, compared against `newCount` - avoids an actual multiply-and-check-overflow instruction sequence), falling through to `xor r13d,r13d` (return NULL) on either guard tripping.

**Conclusion:** the RSZ deserializer, reading from a body whose fields were misaligned by 8 bytes, computed a garbage array element count. That count either failed the overflow guard outright, or (more likely given how deep the recursion went first) was merely *large enough* to eventually exceed available memory or a saner internal limit somewhere in this call chain - either way, this allocator returned NULL, that NULL propagated up to the null-checked call in §8.4, and the null branch's own attempt to log the failure crashed because the retail logger is itself null.

### 8.7 Confirming the fix

The fix implied by all of the above: keep the body at the offset it was serialized for. Verified directly, before ever re-uploading anything, with a throwaway Go probe (deleted after use, per project convention):

```go
// cmd/re2-alignfix/main.go (throwaway, deleted after use)
dec, _ := reengine.Decode(data, reengine.KeyRE2)
fmt.Printf("source: DataOffset=%#x (mod16=%d) hasID=%v\n", dec.DataOffset, dec.DataOffset%16, dec.HasID)

// Keep HasID so the rebuilt body starts at 0x20 - the same absolute
// offset it was serialized for. Dropping it moves the body to 0x18,
// shifting every 16-byte-aligned field by 8 bytes.
out, _ := reengine.Build(dec.Body, reengine.KeyRE2,
    reengine.BuildOptions{HasID: true, SteamID: dec.SteamID})
verify, _ := reengine.Decode(out, reengine.KeyRE2)
if verify.DataOffset%16 != dec.DataOffset%16 {
    panic("alignment NOT preserved")
}
```

Uploaded live: **no crash at all** - a graceful "Can't load the saved data because it is corrupted" dialog instead. That's the game successfully parsing the container and body (alignment now correct) and cleanly rejecting it at its own validation step (the `HAS_ID` flag itself, which native PS5 saves never set - confirmed by also trying with the embedded ID zeroed, which changed nothing). This is exactly the third row of the failure-progression table in §6, and directly confirmed the root cause before the real fix (drop the ID field *and* re-align for `0x18`, §3/§4) was built.

## 9. Notes, dead ends, and honesty about provenance

### 9.1 The register-match coincidence

The first crash capture's `r14` register held `0x5507376f` - which is exactly a real field hash from class `0x3b9a2a09` (the same class involved in the alignment-bug discovery). This briefly looked like direct forensic evidence pointing at one specific field. It wasn't: that hash occurs **3,444 times** in the 39-hour save used for that capture (a per-room "gimmick"/interactable-state field - large counts are just what a late-game save has). The second capture, on a completely different fresh save, showed `r14` holding a value matching no known field hash at all, while every other detail of the crash (signal, thread, fault address, and especially `rip`) stayed identical - confirming the first match was coincidental, not diagnostic. Recorded here because it's an easy trap: a register happening to hold a plausible-looking value is not evidence without checking how common that value actually is.

### 9.2 Provenance of the eboot.bin used

The supplied `eboot.bin` came from a cracked "Unlocked ALL DLC" package obtained outside official channels. The analysis performed on it is legitimate interoperability reverse engineering (understanding a save-format validation routine, on a game already owned on both platforms, for the sole purpose of making the platforms interoperate) - not redistribution, not defeating DRM for piracy purposes, and not code reuse (nothing from the binary was copied into this project; only facts about *what it does at one address* were extracted, the same evidentiary standard applied to every other format in this project). The one provenance question that actually matters technically - whether a modified/cracked binary could make this analysis describe code that isn't what really runs on the console - was checked directly: the crash dump's own register state (`rdi = 0`) matches what the disassembled code at that exact address produces (`xor edi,edi` two instructions prior), which wouldn't line up if this build's code at that address differed from the console's.

### 9.3 What a class-hash symbol table could unlock later

While reading strings near the format-string load in §8.5, the investigation passed through RE Engine's own **reflection symbol table**, present in the executable's rodata: real, human-readable names (`get_SyncTarget`, `via.movie.MovieManager.GCTarget`, `set_StoreEnable`, ...). Since RSZ class/field hashes are murmur3 of these names, hashing every string in that table would let a future pass recover real names for hashes like `0x8b7dd7a1`/`0xb41fa365` locally, without needing a multi-megabyte external community schema dump. Not done - noted here as a concrete, cheap follow-up if the anonymous hex constants ever need to become readable.

### 9.4 Tools actually used, for reference

| Purpose | Tool |
|---|---|
| Locate inner ELF, parse ELF/program headers | Python 3 (`struct.unpack_from`), no library |
| Disassembly | `objdump -D -b binary -m i386:x86-64 -M intel --adjust-vma=<addr>` (from `linuxbrew`) |
| Byte-window extraction for disassembly | `dd if=eboot.bin bs=1 skip=<off> count=<len>` |
| Kernel log capture | raw TCP via bash, `cat < /dev/tcp/<ip>/3232`, backgrounded with `timeout` |
| PS5 save upload/download during testing | `curl` against Garlic's HTTP API (`/api/mount`, `/api/upload_file`, `/api/download_file`, `/api/unmount`) |
| Checked but not used | `capstone` (not installed), `gdb` (present, but this was static file analysis, not a live debugger attach), IDA/radare2/Ghidra (none used) |
| Checked and found irrelevant | community repo `kvasszn/ree-save-editor` had no PS5-specific RE2 handling to cross-reference (only unrelated-cipher PS5 support for a different title) |
