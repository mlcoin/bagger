/*
 * Copyright 2018-Present Alexander Shvid and Contributors
 * Copyright 2017 Dgraph Labs, Inc. and Contributors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */
 
package bagger

import (
	"io/ioutil"
	"math"
	"math/rand"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/bigbagger/bagger/y"
	"github.com/stretchr/testify/require"
)

func val(large bool) []byte {
	var buf []byte
	if large {
		buf = make([]byte, 64)
	} else {
		buf = make([]byte, 16)
	}
	rand.Read(buf)
	return buf
}

func numKeys(db *DB, readTs uint64) int {
	txn := db.NewTransactionAt(readTs, false)
	defer txn.Discard()

	itr := txn.NewIterator(DefaultIteratorOptions)
	defer itr.Close()

	var count int
	for itr.Rewind(); itr.Valid(); itr.Next() {
		count++
	}
	return count
}

func TestDropAll(t *testing.T) {
	dir, err := ioutil.TempDir("", "bagger")
	require.NoError(t, err)
	defer os.RemoveAll(dir)
	opts := getTestOptions(dir)
	opts.managedTxns = true
	db, err := Open(opts)
	require.NoError(t, err)

	N := uint64(10000)
	populate := func(db *DB, start uint64) {
		var wg sync.WaitGroup
		for i := start; i < start+N; i++ {
			wg.Add(1)
			txn := db.NewTransactionAt(math.MaxUint64, true)
			require.NoError(t, txn.Set([]byte(key("key", int(i))), val(false)))
			require.NoError(t, txn.CommitAt(uint64(i), func(err error) {
				require.NoError(t, err)
				wg.Done()
			}))
		}
		wg.Wait()
	}

	populate(db, 1)
	require.Equal(t, int(N), numKeys(db, math.MaxUint64))

	require.NoError(t, db.DropAll())
	require.Equal(t, 0, numKeys(db, math.MaxUint64))

	// Check that we can still write to mdb, and using the same timestamps as before.
	populate(db, 1)
	require.Equal(t, int(N), numKeys(db, math.MaxUint64))
	db.Close()

	// Ensure that value log is correctly replayed, that we are preserving baggerHead.
	opts.managedTxns = true
	db2, err := Open(opts)
	require.NoError(t, err)
	require.Equal(t, int(N), numKeys(db2, math.MaxUint64))
	db2.Close()
}

func TestDropAllRace(t *testing.T) {
	dir, err := ioutil.TempDir("", "bagger")
	require.NoError(t, err)
	defer os.RemoveAll(dir)
	opts := getTestOptions(dir)
	opts.managedTxns = true
	db, err := Open(opts)
	require.NoError(t, err)

	N := 10000
	// Start a goroutine to keep trying to write to DB while DropAll happens.
	closer := y.NewCloser(1)
	go func() {
		defer closer.Done()
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()

		i := N + 1 // Writes would happen above N.
		for {
			select {
			case <-ticker.C:
				i++
				txn := db.NewTransactionAt(math.MaxUint64, true)
				require.NoError(t, txn.Set([]byte(key("key", i)), val(false)))
				_ = txn.CommitAt(uint64(i), nil)
			case <-closer.HasBeenClosed():
				return
			}
		}
	}()

	var wg sync.WaitGroup
	for i := 1; i <= N; i++ {
		wg.Add(1)
		txn := db.NewTransactionAt(math.MaxUint64, true)
		require.NoError(t, txn.Set([]byte(key("key", i)), val(false)))
		require.NoError(t, txn.CommitAt(uint64(i), func(err error) {
			require.NoError(t, err)
			wg.Done()
		}))
	}
	wg.Wait()

	before := numKeys(db, math.MaxUint64)
	require.True(t, before > N)

	require.NoError(t, db.DropAll())
	closer.SignalAndWait()

	after := numKeys(db, math.MaxUint64)
	t.Logf("Before: %d. After dropall: %d\n", before, after)
	require.True(t, after < before)
	db.Close()
}
