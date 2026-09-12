# pacman mirror-abandonment reproducer

`charly.yml` is a **self-contained** charly project: a scratch box (`mirror-repro`) on the
upstream CachyOS image, composing one candy (`pacman-mirror-repro`) whose `run:` steps make real
pacman print the sentence the `pacman-mirror-abandoned-transaction-recovered` allowance claims:

    warning: too many errors from <host>, skipping for the remainder of this transaction

No scratch project, no uncommitted layer, no dependency on a running bed: the reproducer is a
file in this tree, and the log it produces is committed next to it (`build.log`).

## Invocation

    charly -C candy/plugin-check/testdata/pacman-mirror-abandoned/reproducer box build mirror-repro

(charly from `main` is enough — the reproducer does not need this branch's allowlist entry; the
entry is what `../bed_diagnostics_reproducer_test.go` uses to classify the committed log.)

## What the three run steps do, and why each is needed

1. `mirror-repro-sandbox-disabled` — writes `DisableSandboxNetwork` into `/etc/pacman.conf`, the
   precondition the CachyOS base image carries. CachyOS's pacman is a rebuild carrying a scriptlet
   network-isolation patch: when it cannot create its scriptlet network namespace — which a
   rootless build container cannot do — it REFUSES to run every scriptlet and hook and prints
   `error: command failed to execute correctly`. That is a different class; the reproducer takes
   the same one-line fix the cachyos base box takes
   (`charly/charly.yml`: `grep -q '^DisableSandboxNetwork' /etc/pacman.conf || sed -i …`), so the
   only diagnostics left in the log are the ones this candy exists to produce.
2. `mirror-repro-sync` — `pacman -Sy` while every configured mirror is still reachable, so the
   databases are current before the dead server is introduced. This matters: a `.db` retrieval
   failure names no package and therefore no recovery line, so a reproducer that broke the
   database downloads too would produce an error the allowance correctly refuses to claim.
3. `mirror-repro-abandon` — prepends `Server = http://127.0.0.1:1/$repo/os/$arch` to every
   mirrorlist, then reinstalls the glibc/gcc package set into a fresh `--cachedir`, so pacman
   must fetch real payloads, fails three times against the dead server, prints the sentence,
   abandons that server for the remainder of the transaction, and completes from the next mirror.
   The transaction exits 0.

`127.0.0.1:1` is deliberate: it is a REFUSED connection, so each failure is immediate and off the
network, and the reproducer depends on no upstream mirror's state — unlike the run the class was
first observed under, where a CachyOS CDN was 404ing a superseded build.

## Expected lines (all four are asserted by the test)

    error: failed retrieving file 'glibc-2.44+r24+g16be1518495f-1-x86_64_v3.pkg.tar.zst' from 127.0.0.1:1 : Failed to connect to 127.0.0.1:1 after 0 ms: Could not connect to server
    warning: too many errors from 127.0.0.1:1, skipping for the remainder of this transaction
    checking keyring...
    upgrading glibc...

The verb on the last line is the honest variable: pacman says `upgrading` when the repository
carries a newer build than the image (the case here), and `is up to date -- reinstalling` when the
image is already at the repository's version. Both are recovery lines the error-tier
`pacman-mirror-retrieval-recovered` allowance accepts, and both warning verbs have their own
allowance entry — which is why the classification does not depend on which one a given day's
repository state produces.

## build.log provenance

`build.log` is the COMPLETE stdout+stderr of the invocation above: 165 lines, 13387 bytes,
sha256 `c089ce7ed94fe9f7c5cf6b91ab0224b9f6a49d783d842fec46042ae8c2bb7171`, captured 2026-09-12 with
charly 2026.255.2200 (a dev build of `main` plus this branch) on a CachyOS host; the box tag is
`2026.255.1825`.

It is complete rather than trimmed on purpose, and the test enforces it: a later edit that
replaced it with a hand-picked excerpt would drop the build's completion line
(`Successfully tagged localhost/mirror-repro:…`) and fail `must carry the run's own lines`
rather than pass review unnoticed. Two of the retrieval errors and one of the two abandonment
sentences in it come from a real CachyOS CDN (`cdn77.cachyos.org`) 404ing a superseded build —
the upstream condition the class was first observed under; the other ten errors and the other
sentence come from the dead server this reproducer introduces.

## Composing it into a check bed (the R10 live proof)

The bed run quoted in the proof section of the PR does not run THIS project: it composes this candy
into the bed under test so the bed's `image-build` step log carries the sentence. The whole recipe:

1. append this directory's `charly.yml` candy node — the `pacman-mirror-repro:` node, from that
   key to the end of the file — to the bed project's own `charly.yml`;

2. add ONE line to the box under test (distro-cachyos: `box/immich-ml/charly.yml`), after the end
   of its candy list:

   ```yaml
   - pacman-mirror-repro
   ```

3. run the bed:

   ```
   charly check run check-cachyos-immich-ml-pod
   ```

With the two `pod-immich-ml` pin lines at the MERGED tag `v2026.255.1229`
(`box/immich-ml/charly.yml` and `candy/charly-marketplace/charly.yml` — the merged
opencharly/pod-immich-ml release, which is also what clears the unrelated
`resolved to multiple versions` class), that composition is the ENTIRE delta from the project's
`main`: 2 pin lines + 1 composed candy. `git diff --stat` reports exactly those three files and no
fourth path is modified.
