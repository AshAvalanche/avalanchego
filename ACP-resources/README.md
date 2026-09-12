# Warp UTXOs

A C-Chain address, contract or EOA, owns AVAX on the P-Chain and stakes it. A
UTXO's spending condition is satisfied by an Avalanche Warp message carrying the
exact transaction to run, instead of a secp256k1 signature.

Branch: `acp-warp-owner`.

## Setup

Go >= 1.25.10, gcc, g++. On macOS, a modern bash.

```bash
./scripts/build.sh        # the node
./scripts/build_xsvm.sh   # required: without it a tmpnet network never boots
```

Rebuild **both** whenever you rebuild either. A stale plugin fails the whole
network with `RPCChainVM protocol version mismatch`.

## Unit tests

```bash
go test ./vms/warpfx/... ./vms/platformvm/... ./vms/saevm/...
./scripts/run_task.sh lint       # golangci-lint plus the repo's own checks
```

BUILD files are consensus for CI, which runs `bazel test //...`:

```bash
go install github.com/bazelbuild/bazelisk@latest
bazelisk run //:gazelle        # regenerate after adding or moving a file
bazelisk test //:gazelle_test  # what CI checks
```

Neither nix nor `task` is needed; `run_task.sh` bootstraps `task` through
`go tool`, and `nix_run.sh` execs anything already on PATH. Upstream gazelle
installed from Go is **not** a substitute: it drops every libevm dependency.

## End-to-end tests

```bash
./bin/ginkgo -v --focus="Warp" ./tests/e2e -- \
    --avalanchego-path=$PWD/build/avalanchego \
    --node-count=5 --activate-latest-after=90s
```

Six specs, about three minutes on a five-node local network.

`--activate-latest-after` is what makes it work: it schedules Helicon in the
near future rather than at genesis, so the C-Chain still runs coreth when the
suite starts. Without it the specs skip, and the one assertion that coreth
*cannot* encode a warp output is never reached.

Add `--reuse-network` to iterate, `--stop-network` to tear down. A fresh network
per run is safer for a demo: a reused one starts with Helicon already active and
an extra validator.

## What the tests establish

| Spec                           | File                                        | Covers                                                                                                                                            |
| ------------------------------ | ------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------- |
| `[Warp UTXOs]`                 | `tests/e2e/p/warp_utxos.go`                 | the full round trip, **EOA** owner; coreth refusing a warp export before the VM transition, and the same bytes passing after                       |
| `[Warp UTXOs Contract]`        | `warp_utxos_contract.go` + `warp_owner.sol` | the same, owned by a **Solidity contract**: it owns, stakes and recovers AVAX with no key, and learns its own txID from a `TxExecuted` attestation |
| `[Warp UTXOs Mixed]`           | `warp_utxos_mixed.go`                       | a signed input and an authorized input **in one transaction**, and the refusal of a second owner under a single authorization                      |
| `[Warp Export Precompile]` ×2  | `warp_export_precompile.go`                 | `exportAVAX()`, the round trip for an **EOA** and a **contract** moving their *own* balance                                                        |
| `[Warp Staking Family]`        | `warp_staking_family.go`                    | a **delegation**, and an **auto-renewed validator** whose authority is a warp owner                                                               |

Unit tests sit beside the code they cover:

- `vms/warpfx/` — the types, the Fx, the fee band;
- `vms/platformvm/txs/executor/` — authorization, canonical imports, the
  activation and lock guards, export destination, rewards, delegation;
- `vms/platformvm/txs/fee/` — pricing the credential;
- `vms/saevm/cchain/tx/` — codec alignment, the C-Chain canonical import;
- `vms/saevm/cchain/precompile/nativeexport/` — the export precompile.

Three codec tests pin invariants no compiler protects — positions 43/44/45, and
the agreement between the transaction, block and atomic codecs:

```bash
go test ./vms/platformvm/txs/ ./vms/platformvm/block/ ./vms/saevm/cchain/tx/ -run Codec
```

## The two folders

**`implementation/`** — the design, lot by lot, in the order it was built:
types, codecs, Fx dispatch, authorization, canonical imports, guards,
attestations, discovery, pricing, the saevm side, the precompile, the tests. Each
guide says what to write, why that way rather than another, and what writing it
turned up. Start at `implementation/README.md`.

**`workflow/`** — one file per transaction path, describing what actually
happens: what is built, by whom, what is verified, in what order, and what
changes in state. No history and no dates; when the code changes, the file is
corrected in place. Start at `workflow/README.md`.

Read `workflow/` to understand a path, `implementation/` to understand a
decision.
