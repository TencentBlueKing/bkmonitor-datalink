# Windows acceptance

Scope: Windows 10/11 x64, ordinary user, local fixed NTFS volume,
PowerShell 5.1 and 7. The portable executable uses no CGO runtime.

## Local verification, 2026-10-08

| Check | Observed result |
| --- | --- |
| Windows 11 10.0.26200, non-elevated user, Go 1.26.0 | Native CLI tests, vet and module verification passed |
| Windows executable fixtures | HTTP/private CA login, file input, partial exit 3, concurrent renewal once, logout and error exits passed |
| Windows filesystem | Protected user/SYSTEM DACL, process lock release after kill, concurrent updates, replacement failure preserving old data, junction/hard-link rejection passed |
| PowerShell 5.1.26100.9444 / 7.6.5 | Archived executable help/version passed without creating configuration |
| Linux amd64, Go 1.26.0 container | CLI tests, vet and race passed |
| OB/Redis blackbox, Linux local checkout of current CLI sources | All transport, multi-instance RPC and Slot suites passed; Redis 7.2.10; fixtures are not live business acceptance |
| Three target archives | Built; SHA256 checks passed; Linux and Windows archived executables started; native and cross-built Windows executable hashes matched |
| GitHub configuration | actionlint 1.7.12 passed; no remote pipeline run |

Windows 10, native macOS execution, interactive hidden input/Ctrl+C, browser
callback, second-user access denial and Windows-to-deployment acceptance remain
pending. Automated Windows fixture success does not complete those gates.

## Automated gates

| Gate | Command / evidence | Required result |
| --- | --- | --- |
| Native CLI tests | `go test -count=1 -timeout=10m ./...` | Existing contracts, DACL, replacement, process lock, renewal and real executable fixtures pass |
| Static checks | `go mod verify`; `go vet ./...` | Pass |
| Unix regression | Native Linux/macOS CLI tests; Linux race | Pass |
| Server contract | `bash integration/ob-channel/run.sh` on Linux with Redis | Independent module passes; fixtures are not live business acceptance |
| Candidate archives | SHA256SUMS, BUILD-INFO, native help/version | All three targets pass using the archived executable |

The GitHub workflow automates these gates. A Windows Server runner does not
establish Windows 10/11 desktop support. CI configuration must run successfully
before claiming remote CI validation; merely adding YAML is not that evidence.

## Desktop and deployment gates

Record OS version, shell version, executable hash, test outcome and sanitized
evidence for each case. These gates remain pending until executed.

| Case | Procedure | Required result |
| --- | --- | --- |
| Portable startup | Extract ZIP as ordinary user; run in both shells | help/version succeed with no configuration or network |
| Hidden authorization input | Paste disposable fixture authorization in `auth login` | No echo; successful login; no secrets in stdout/stderr |
| Interrupt | Ctrl+C during hidden input and `auth listen` | Console input works afterward; exited listener port is reusable |
| UTF-8 paths | Use Chinese/space config, input and private CA paths | JSON and absolute evidence paths remain correct |
| User isolation | Try reading profiles, lock, temporary and result files as a second ordinary user | Access denied; owner can still operate; do not use an administrator token to prove isolation |
| Browser callback | Use the trusted deployment authorization page in Edge/Chrome | Successful loopback login; wrong origin/state refused |
| Deployment contract | Windows binary against Linux OB/Redis deployment: login, discover, describe, invoke, status, renewal, logout | Correct identities, one renewal, valid redacted evidence, confirmed revocation |
| Partial/error contract | Exercise partial response and invalid input | Exit 3/2 respectively; evidence and JSON retain their existing meaning |

Do not change profile schema or server protocols for Windows. Roll back by
replacing the executable with a previously validated Windows build; existing
profiles and evidence retain their schema. Local failures after remote success
do not undo remote credential changes.
