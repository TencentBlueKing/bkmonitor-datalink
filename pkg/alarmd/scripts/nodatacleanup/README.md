# nodatacleanup

Removes the whole-memory no-data records that the per-group representation
replaced (decision-008). Run **once**, by hand, after every worker has been on a
build that writes the per-group record for longer than one rollout window.

It is not a migration. Nothing is copied across: each Plan writes its new record
on the first round it runs. What is left behind is the old record, which nothing
reads once the new one is at least as new.

## Running it

Enumerate first. This writes nothing and is the run whose output gets read:

```
go run ./pkg/alarmd/scripts/nodatacleanup \
  -address <host>:<port> -db <n> -prefix <state key prefix>
```

It prints six counts:

```
enumerated=N with_per_group_record=M eligible=K deleted=0 unreadable=U vanished=V
```

The gaps are the reading, not the totals:

| gap | what it is |
|---|---|
| `enumerated - with_per_group_record` | Plans that have not run since the upgrade. Their only memory is the record being considered, so it stays. |
| `with_per_group_record - eligible` | Plans whose old record is the **newer** one. During a rollout that is a Plan that went back to a build writing the old record; deleting it would lose those rounds. |
| `unreadable` | Bytes nobody can decode. Skipped, never deleted: the version check cannot be run on it, and it is the record most worth keeping. |
| `vanished` | Enumerated and gone by the time it was read. Its generation expired on its own. |

If `with_per_group_record` is far below `enumerated`, the fleet has not finished
rolling. Wait rather than delete.

Then delete. `-copy-to` is required, because the delete is the irreversible
half:

```
go run ./pkg/alarmd/scripts/nodatacleanup \
  -address <host>:<port> -db <n> -prefix <state key prefix> \
  -delete -copy-to ./nodata-blobs.bak
```

Every record is `DUMP`ed and its serialisation written and `fsync`ed **before**
the `DEL`. A copy that fails to write stops the whole run rather than skipping
that key. The run ends by printing how many records were saved and where.

## Putting them back

One file, one record per line, `key<TAB>base64(DUMP)`. One file rather than a
directory of key-named files on purpose: the copy is appended and synced per
record, so a run that stopped halfway leaves a complete file rather than a
directory whose last entry may be partial, and one artefact is easier to move to
wherever the restore is run from than several thousand files whose names are
120-character keys.

```
while IFS=$'\t' read -r key payload; do
  redis-cli -h <host> -p <port> -n <db> --no-raw \
    RESTORE "$key" 0 "$(printf %s "$payload" | base64 -d)"
done < ./nodata-blobs.bak
```

`RESTORE` refuses a key that already exists, which is the behaviour to want
here: a Plan that has since written a new old-shape record must not be
overwritten by one from before the cleanup. Add `REPLACE` only if you have
established that is what you mean.

The restored records carry no TTL (`0` above). They are generation-scoped keys
and the load path renews them, so the first Plan that reads one gives it a
lifetime again.
