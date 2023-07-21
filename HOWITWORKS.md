# Technical Information

- [How does pruning work?](#how-does-pruning-work)
  - [application.db](#applicationdb)
    - [Data structure](#data-structure)
    - [Pruning strategy](#pruning-strategy)
  - [blockstore.db](#blockstoredb)
  - [state.db](#statedb)
  - [txindexer.db](#txindexerdb)

## How does pruning work?

Pocket Pruner prunes each of the following goleveldb directories independently.
Each database has its own structure.  This document describes those structures
and how Pocket Pruner prunes them.

- application.db
- blockstore.db
- state.db
- txindexer.db

### application.db

#### Data structure

application.db is the world state database of the Pocket Network, including
staked nodes, staked apps, token balances, and etc.  The basic structure is an
[IAVL+](https://github.com/cosmos/iavl) tree per block height.  IAVL+ tree is
a versioned data structure, so Pocket leverages that versioning to store
per-block data.  The root hash of this tree is called `AppHash`, which is stored
in the block header and used to secure the blockchain along with other hashes
like block hash.

Application data is categorized into the following subspaces and stored
separately.  For example, the balance of an account at block 1000 is stored
under the subspace `auth` in a tree of version 1000.

Below is the list of subspaces.

- `application`: Staked applications
- `auth`: Token supply and balances
- `gov`: Unused
- `main`: Tendermint's consensus parameters
- `params`: Pocket parameters
- `pocketcore`: Pocket claims
- `pos`: Staked nodes

To express the application data above, application.db consists of records
associated with the following keys.

- `s/latest` - Latest version
- `s/<version>` - Commit info
- `s/k:<subspace>/r<version>` - Root
- `s/k:<subspace>/n<hash>` - Node
- `s/k:<subspace>/o<versionTo><versionFrom><hash>` - Orphan

The Latest version record simply indicates the latest height in the database.

The Commit info records store the root hashes of all subspaces.

The Root records store root hashes of trees.  The hashes in this record equal
to the hashes stored in Commit Info.  For example, if a hash of the `params`
subspace in Commit info of version X is A, the value of `s/k:params/r` of the
version X must be A.

The Node records represent branch and leaf nodes.  These records can be shared
by multiple versions.  For example, if no change is made in Pocket parameters
between block X and X+1, all nodes of the `params` tree at block X are reused
by the tree at block X+1 and thus the root hash of version X+1 ends up having
the same root hash as the tree of version X.

The Orphan records represent nodes that are deleted but persisted in other
versions.

#### Pruning strategy

Pruner iterates all records in application.db and decides whether to prune or
keep.  What Pruner keeps is

- Latest version record
- Commit info records at and after the specified height
- Root records at and after the specified height
- Node records traversed from the Root records to keep
- Orphan records where `<versionTo>` is equal or greater than the specified
  height

### blockstore.db

blockstore.db stores the actual block data of the Pocket Network blockchain.
It consists of five types of data associated with the following keys.

- `H:<version>` - Block Meta
- `P:<version>:<index>` - Block Part
- `C:<version>` - Block Commit
- `SC:<version>` - Seen Commit
- `BH:<hash>` - Block Hash

The first four types of data are versioned.  Pruner keeps those data at and
greater than the specified height, and all Block Hash records.

### state.db

state.db stores state information in the Tendermint layer.  It consists of
three types of data associated with the following keys.

- `validatorsKey:<version>` - Validators
- `consensusParamsKey:<version>` - Consensus Params
- `abciResponsesKey:<version>` - ABCI Response

Because all records are versioned, pruner simply keeps data at and greater than
the specified height.

### txindexer.db

txindexer.db stores transaction (=tx) results indexed by height, signer/recipient
address, and hash in order to provide access to transaction results without
searching blocks.  It consists of four types of data associated with the
following keys.

- `tx.height/<height>/<index>` - Tx hash indexed by height
- `tx.signer/<signer>/<height>/<index>` - Tx hash indexed by signer
- `tx.recipient/<recipient>/<height>/<index>` - Tx hash indexed by recipient
- `<txhash>` - Transaction result indexed by Tx hash

This data is used in two cases.

1. Prevent replay attack
2. Serve transaction queries

To prevent replay attack, a Pocket node does not accept a transaction if its
hash has been already accepted.  When a node receives a transaction, it looks up
its hash in txindexer.db and rejects it if the hash exists.

The second case is straightforward.  When you query transactions via
`/v1/query/blocktxs`, `/v1/query/accounttxs`, `/v1/query/tx`, or equivalent
`pocket query` commands, the node searches txindexer.db for matching
transactions.  Without txindexer.db, these queries do not work.

For the first purpose, what a node needs is whether a matching transaction
exists or not.  This means, if the second purpose can be sacrificed, we can
reduce the size of txindexer.db drastically by dropping all transaction
results.  Therefore, what Pocket Pruner does is to replace all transaction
results with an empty object regardless of the specified height.
