# Pocket Pruner <!-- omit in toc -->

- [How to prune data](#how-to-prune-data)
- [Pruning rules](#pruning-rules)
- [Example commands](#example-commands)
- [Verification Mode (for developers only)](#verification-mode-for-developers-only)
- [FAQ](#faq)
  - [How does pruning work?](#how-does-pruning-work)
  - [What is a `version`?](#what-is-a-version)
  - [Why do we still use Amino](#why-do-we-still-use-amino)

Pocket Pruner is an offline pruner of [Pocket Network](https://www.pokt.network/)
developed by [C0D3R](https://c0d3r.org/).

As of writing, a full node of Pocket Network requires about `700 GB` of disk space
to store the historic blockchain data from the genesis and grows by about `2 GB/day`.
This means that all node runners will continuously need to expand storage space.

Pocket Pruner solves this problem by **'pruning'** the data directory. Pruning is
the process of erasing older data to save disk space. Because such old data is
needed in limited cases only (See the ["Pruning rules"](#pruning-rules) below),
not all nodes have to keep full data from the genesis (a.k.a. archival nodes in other
chains). Node runners can run this tool offline to bring the total storage back down
to `100 GB` or less in about `2-3 hours`. This step can be done periodically to keep
the total disk storage within a limited range. Once pruning is done,
`pruning pruned data again takes about an hour`.

## How to prune data

Data of Pocket Network consists of the following [goleveldb](https://github.com/syndtr/goleveldb) database directories.
Pocket Pruner processes each database independently. It does not touch other
files or directories such as pocket_evidence.db or cs.wal.

- dataDir/application.db
- dataDir/blockstore.db
- dataDir/state.db
- dataDir/txindexer.db

To start pruning, you stop the pocket-core process and execute the following
command.

```
pruner <pruneBeforeBlock> <dataDir> <databases>

Where
  pruneBeforeBlock:
    specifies the oldest height to keep in the data directory.

  dataDir:
    specifies the path to the data directory.
    By default it's ~/.pocket/dat unless overriden with the `--datadir` option.

  databases:
    specifies the names of databases to prune as a comma-separated list.  The
    supported names are `application`, `blockstore`, `state`, and `txindexer`.
```

For example, the following command erases all data before block 80000.
Please note that with this command, data at block 79999 and older is erased, and
data at block 80000 and newer is kept. See the ["Example commands"](#example-commands)
for the actual pruning scenario.

```bash
pruner 80000 ~/.pocket/data application,blockstore,txindexer,state
```

⚠️ Pruning full data could take `2-3 hours`, or more time depending on the
parameters to pass, to finish. Once finished, the following new directories are
created side by side with the original directories. Pocket Pruner does not
modify the original directories, which means that a node to be pruned **needs
to have extra space to keep both the original data and pruned data at the same
time**. ⚠️

- dataDir/application-new.db
- dataDir/blockstore-new.db
- dataDir/state-new.db
- dataDir/txindexer-new.db

Then the following commands replace the data directories with the pruned ones.

```bash
rm -rf dataDir/application.db
mv dataDir/application-new.db dataDir/application.db

rm -rf dataDir/blockstore.db
mv dataDir/blockstore-new.db dataDir/blockstore.db

rm -rf dataDir/state.db
mv dataDir/state-new.db dataDir/state.db

rm -rf dataDir/txindexer.db
mv dataDir/txindexer-new.db dataDir/txindexer.db
```

After switching directories, you can start the pocket-core process in a normal way.

## Pruning rules

1. This is an offline tool. The pocket-core process **MUST** be stopped while pruning.

2. **DO NOT** run pruned data on a node that is pointed as an external gateway of
   Pocket Network (Chain ID 0001) in `chains.json` because any Pocket gateway may
   receive relays to query for any block at any time.

3. When a node processes and validates a proof transaction, it needs to access
   information at the session height of the proof's corresponding claim. This
   means all nodes, even if not staked, need to have their `application.db` have
   blocks at all valid (i.e. active) session heights. Otherwise the node will stop
   syncing.

   _We **RECOMMEND** keeping at least `pos/BlocksPerSession * pocketcore/ClaimExpiration`
   blocks in `application.db`, which is currently 96 blocks in Pocket MainNet._

4. A tendermint block contains a field named [Evidence](https://github.com/tendermint/tendermint/blob/main/spec/consensus/evidence.md) that stores duplicated vote (i.e. double-sign)information if detected.
   When a node validates a proposed block with `Evidence`, it needs to access
   information at the height of its duplicated votes. The expiration period of
   `Evidence` is defined as the tendermint's parameter `ConsensusParams.Evidence.MaxAge`,
   which is currently `120000000000`in Pocket MainNet. This means that `Evidence` in
   Pocket MainNet never expires. If the node fails to validate the duplicated
   vote information because of pruning, the Pocket process stops running.

   _For this reason, we **RECOMMEND** not pruning `state.db`._

## Example commands

```bash
# Query the height
$ pocket query height
2023/07/06 01:23:02 Initializing Pocket Datadir
2023/07/06 01:23:02 datadir = /home/ubuntu/.pocket
http://localhost:8082/v1/query/height
{
    "height": 100010
}

# Stop the pocket node
$ sudo systemctl stop pocket

# Inspect the current disk usage; contains only unpruned data
$ du -d1 -h .pocket/data
26G     .pocket/data/state.db
1022M   .pocket/data/cs.wal
323G    .pocket/data/application.db
171G    .pocket/data/blockstore.db
182G    .pocket/data/txindexer.db
6.6M    .pocket/data/evidence.db
702G    .pocket/data

# Start the pruner as a background process
$ nohup ./pruner 99800 ~/.pocket/data application,blockstore,txindexer >prune.log&

# Follow the pruner logs
$ tail -f prune.log
2023/07/06 01:24:14 Pruning before block: 99800
2023/07/06 01:24:15 application.db - s/k:application/
2023/07/06 01:24:35 application.db - s/k:auth/
2023/07/06 01:25:13 application.db - s/k:auth/ 100000000
2023/07/06 01:25:50 application.db - s/k:auth/ 200000000
2023/07/06 01:26:27 application.db - s/k:auth/ 300000000
2023/07/06 01:27:07 application.db - s/k:auth/ 400000000
2023/07/06 01:27:47 application.db - s/k:auth/ 500000000
2023/07/06 01:28:23 Done - blockstore.db
...
2023/07/06 02:14:26 application.db - s/k:pos/ 1700000000
2023/07/06 03:57:29 Done - application.db
2023/07/06 03:57:29 Completed all tasks.

# Inspect the new disk usage; contains both pruned and unpruned data
$ du -d1 -h .pocket/data
26G     .pocket/data/state.db
1022M   .pocket/data/cs.wal
595M    .pocket/data/blockstore-new.db
323G    .pocket/data/application.db
1.2G    .pocket/data/application-new.db
171G    .pocket/data/blockstore.db
19G     .pocket/data/txindexer-new.db
182G    .pocket/data/txindexer.db
6.6M    .pocket/data/evidence.db
722G    .pocket/data

# Replace the original data with the pruned data
$ rm -rf .pocket/data/application.db
$ mv .pocket/data/application-new.db .pocket/data/application.db
$ rm -rf .pocket/data/blockstore.db
$ mv .pocket/data/blockstore-new.db .pocket/data/blockstore.db
$ rm -rf .pocket/data/txindexer.db
$ mv .pocket/data/txindexer-new.db .pocket/data/txindexer.db

# Inspect the new disk usage; contains only pruned data
$ du -d1 -h .pocket/data
26G     .pocket/data/state.db
1022M   .pocket/data/cs.wal
1.2G    .pocket/data/application.db
595M    .pocket/data/blockstore.db
19G     .pocket/data/txindexer.db
6.6M    .pocket/data/evidence.db
47G     .pocket/data

# Start the pocket node
$ sudo systemctl start pocket

# Query the height again to make sure the node is running
$ pocket query height
2023/07/06 07:27:31 Initializing Pocket Datadir
2023/07/06 07:27:31 datadir = /home/ubuntu/.pocket
http://localhost:8082/v1/query/height
{
    "height": 100010
}

# Querying a pruned block returns a null block
$ curl -X POST -d '{"height":99799}' localhost:8082/v1/query/block
{"block":null,"block_id":{"hash":"","parts":{"hash":"","total":"0"}}}
```

## Verification Mode (for developers only)

After pruning is completed, having the original and pruned data side-by-side,
you can verify the pruned data by running Pocket Pruner in verification mode.
The way to run Pocket Pruner in verification mode is to append a bang (!) to
the database name.

Verification is done by loading the original database and checking records that
should not be pruned surely exist in the pruned database. Therefore **verification
takes the same amount of time as pruning**.

```
# Verification mode requires both the original and pruned directories
$ du -d1 -h ~/.pocket/data
2.5M    /home/john/.pocket/data/state.db
28K     /home/john/.pocket/data/evidence.db
206M    /home/john/.pocket/data/cs.wal
40K     /home/john/.pocket/data/txindexer.db
72M     /home/john/.pocket/data/blockstore.db
75M     /home/john/.pocket/data/application.db
1.6M    /home/john/.pocket/data/application-new.db
24K     /home/john/.pocket/data/txindexer-new.db
1.6M    /home/john/.pocket/data/state-new.db
8.8M    /home/john/.pocket/data/blockstore-new.db
366M    /home/john/.pocket/data

# Start the pruner in verification mode to verify all of four databases
# (In bash, you need to escape bang characters)
$ /data/bin/pruner 74000 ~/.pocket/data txindexer\!,state\!,blockstore\!,application\!
2023/07/15 05:23:45 Pruning before block: 74000
2023/07/15 05:23:45 Checked 36 records in TxIndexer.  All good.
2023/07/15 05:23:45 Checked 225029 records in State.  All good.
2023/07/15 05:23:45 Checked 375042 records in BlockStore.  All good.
2023/07/15 05:23:45 Checked 1227726 records in Application.  All good.
2023/07/15 05:23:45 Completed all tasks.
```

This verification mode is for the development purpose. Node runners **MAY** use
verification mode for their fleet, but **it's not necessary for the pruning
purpose**.  If pruning is completed successfully, you can assume the pruned data
is consistent and safe to run.

## FAQ

### How does pruning work?

This work is being tracked in [#3](https://github.com/msmania/pocket-pruner/issues/3).

### What is a `version`?

The words `version` and `height` are used interchangeably in the code. This is legacy that was adopted from Cosmos' [iavl](https://github.com/cosmos/iavl) and [CometBFT (formerly known as Tendermint)](https://github.com/cometbft/cometbft).

### Why do we still use Amino

Even though [pocket-core](https://github.com/pokt-network/pocket-core) no longer uses Amino, [iavl](https://github.com/cosmos/iavl) still does and therefore remains as a dependency.
