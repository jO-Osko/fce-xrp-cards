# Cleaning up the repository

Two steps: stop tracking files that should never have been committed, then squash the whole history into one commit.
`.gitignore` already lists those files (wallet seeds, the watcher's state files, the Testnet archive, build artifacts), so re-adding everything leaves them out.

## Squash all commits into one

```bash
# 1. New branch with no history; the current files stay staged.
git checkout --orphan squashed

# 2. Drop the files that shouldn't be committed; re-adding everything
#    respects the new .gitignore.
git rm -r --cached -q .
git add -A

# 3. One commit with everything.
git commit -m "XRPL credit card on Flare Confidential Compute"
```

Before going further, check that no secrets or state files made it in:

```bash
git ls-files | grep -E 'wallets|archive|watcher/.*\.json'
```

It should list only `watcher/package.json` and `watcher/package-lock.json`.

```bash
# 4. Replace master with it and push.
git branch -D master
git branch -m master
git push --force origin master
```

## Things to know

- **`--force` overwrites the history on GitHub.** Anyone who cloned the repository has to clone it again or reset their copy.
- **Old commits can linger.** GitHub may keep serving them by their hash for a while, and existing forks and clones still have them. The four Testnet wallets whose seeds were committed (`watcher/archive/testnet/wallets.json`) stay burned: never reuse them.
- **To keep the original first commit's date and message instead**, run `git reset --soft $(git rev-list --max-parents=0 HEAD)` and then `git commit --amend`. The orphan branch above is simpler and leaves nothing of the old history behind.

## Only stop tracking the files, keeping the history

If you don't want to squash, this removes the files from the next commit and keeps your local copies:

```bash
git rm -r --cached watcher/archive \
  watcher/broker-log.json watcher/conversions.json watcher/last-settle.json \
  watcher/loan-payments.json watcher/lp-jobs.json watcher/repayment-accounts.json \
  watcher/settle-progress.json watcher/settlements.json \
  web/tsconfig.tsbuildinfo web/package-lock.json
git commit -m "Stop tracking local state and secrets"
```

The seeds then stay in the earlier commits, so the Testnet wallets remain burned either way.
