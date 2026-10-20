# Signing

Commits and annotated tags are verified against the GPG and SSH keys users register, and the instance holds its own OpenPGP key for commits it writes on your behalf.

## Verification states

Every commit and tag the api returns carries a `verification` with one of these states, and the web shows the matching badge.

| State | Badge | Meaning |
| --- | --- | --- |
| `UNSIGNED` | none | The object has no signature |
| `VERIFIED` | Verified | Signed by a registered key whose owner matches the committer, `signer` is their username and `key_id` the key |
| `INSTANCE` | Instance | Signed by this instance's key |
| `UNVERIFIED` | Unverified | Signed but not trusted, `reason` says why |

Both `VERIFIED` and `INSTANCE` satisfy the require signed commits rule in [Branches](branches.md).

## Registering a GPG key

Paste an armored public key under Settings > Keys, call `KeyService.AddGPGKey`, or run `dfcli gpg add [file]`. Private keys are rejected, a key id registers once per instance, and the key id, fingerprint, uid emails, and expiry are read from the key.

The response carries a challenge of the form `distroface-gpg-verify-<random>`. Until it is signed the key stays unverified and commits it signs report `signing key not verified by its owner`. Sign it on the machine holding the private key and submit the armored signature under Settings > Keys > the key > Verify, or with `KeyService.VerifyGPGKey`:

```bash
printf '%s' 'distroface-gpg-verify-...' | gpg --armor --detach-sign --default-key KEYID
```

Calling `VerifyGPGKey` with an empty signature issues a fresh challenge. `dfcli gpg add` prints the challenge, `dfcli gpg list` and `dfcli gpg rm id` manage the keys.

A GPG signature verifies when the issuing key id, primary or subkey, is registered, the signature checks against it, the key is verified, the committer email appears in the key's uids, and that email equals the key owner's account email. Any other outcome is `UNVERIFIED` with a reason such as `committer email does not match the key`.

## Registering an SSH signing key

Add the key under Settings > Keys with usage `signing` or `both`, or run `dfcli key add [file] --signing`. Keys with usage `auth` only never verify signatures. Then point git at it:

```bash
git config --global gpg.format ssh
git config --global user.signingkey ~/.ssh/id_ed25519.pub
git config --global commit.gpgsign true
```

An SSH signature verifies when it is in `SSH SIGNATURE` armor with namespace `git`, uses sha256 or sha512, checks against the embedded public key, that key's fingerprint belongs to a user key registered for signing, and the committer email equals the owner's account email. Deploy keys never verify signatures.

## Instance signing key

| Setting | Default | Description |
| --- | --- | --- |
| `git.signing.enabled` | `false` | Sign commits and tags the server writes with the instance key |

The key is an ed25519 OpenPGP key named `Distroface (instance signing key) <noreply@distroface.local>`, generated on first use at `<storage.data_dir>/keys/signing.asc`. Its public half is shown under Admin > Git > Instance > Signing public key and returned as `signing_public_key` by `SettingsService.GetGitServerInfo`, import it with `gpg --import` to verify locally.

When signing is on, the server signs web commits from `GitFileService.UpdateFiles`, reverts and cherry picks, the initial commits of `auto_init` and templates, every pull request merge including squash, rebase, and rebase merge commits, and annotated tags created with `GitRefService.CreateTag`. Those objects report `INSTANCE`. Objects written while signing is off stay unsigned. A signature made by the instance key is recognized even after the setting is turned off again.

## How verification runs

Signatures are checked against the stored keys, reloaded every five minutes and at once when a key is added, removed, or verified. Outcomes are memoized per object for a minute. Tags are verified the same way through `GetTag` and `ListGitTags`.

| Reason | Cause |
| --- | --- |
| `no known key signed this object` | No registered key matches the signature |
| `signing key not verified by its owner` | The GPG challenge was never signed |
| `committer email does not match the key` | The email is not a uid on the key |
| `committer email does not match the key owner` | The email differs from the account that registered the key |
| `signing key is not registered for signing` | SSH key is unknown, a deploy key, or registered for auth only |
| `signature namespace is not git` | SSH signature made for another purpose |
| `key owner not found` | The owning account was deleted |
