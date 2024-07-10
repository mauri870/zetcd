// Copyright 2016 CoreOS, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package zetcd

import (
	"go.etcd.io/etcd/client/v3"
)

func statGetsRev(p string, rev int64) []clientv3.Op {
	return []clientv3.Op{
		clientv3.OpGet(mkPathCTime(p), clientv3.WithSerializable(), clientv3.WithRev(rev)),
		clientv3.OpGet(mkPathMTime(p), clientv3.WithSerializable(), clientv3.WithRev(rev),
			clientv3.WithSort(clientv3.SortByModRevision, clientv3.SortDescend)),
		clientv3.OpGet(mkPathKey(p), clientv3.WithSerializable(), clientv3.WithRev(rev)),
		clientv3.OpGet(mkPathCVer(p), clientv3.WithSerializable(), clientv3.WithRev(rev)),
		clientv3.OpGet(mkPathACL(p), clientv3.WithSerializable(), clientv3.WithRev(rev), clientv3.WithKeysOnly()),
		// to compute num children
		clientv3.OpGet(getListPfx(p), clientv3.WithSerializable(), clientv3.WithRev(rev), clientv3.WithPrefix()),
	}
}

func statGets(p string) []clientv3.Op { return statGetsRev(p, 0) }

func statTxn(p string, txnresp *clientv3.TxnResponse) (s Stat, err error) {
	ctime := txnresp.Responses[0].GetResponseRange()
	mtime := txnresp.Responses[1].GetResponseRange()
	node := txnresp.Responses[2].GetResponseRange()
	cver := txnresp.Responses[3].GetResponseRange()
	aver := txnresp.Responses[4].GetResponseRange()
	children := txnresp.Responses[5].GetResponseRange()

	// XXX hack: need to format zk / node instead of this garbage
	if len(ctime.Kvs) != 0 {
		// Ctime is stored in microseconds
		s.Ctime = decodeInt64(ctime.Kvs[0].Value) / 1000
		s.Czxid = rev2zxid(ctime.Kvs[0].ModRevision)
		s.Pzxid = s.Czxid
	}
	if len(mtime.Kvs) != 0 {
		s.Mzxid = rev2zxid(mtime.Kvs[0].ModRevision)
		// Mtime is stored in microseconds
		s.Mtime = decodeInt64(mtime.Kvs[0].Value) / 1000
		s.Version = Ver(mtime.Kvs[0].Version - 1)
	}
	if len(cver.Kvs) != 0 {
		s.Cversion = Ver(cver.Kvs[0].Version - 1)
		s.Pzxid = rev2zxid(cver.Kvs[0].ModRevision)
	}
	if len(aver.Kvs) != 0 {
		s.Aversion = Ver(aver.Kvs[0].Version - 1)
	}
	if len(node.Kvs) != 0 {
		s.EphemeralOwner = Sid(node.Kvs[0].Lease)
		s.DataLength = int32(len(node.Kvs[0].Value))
	}
	s.NumChildren = int32(len(children.Kvs))

	if p != "/" {
		if s.Ctime == 0 {
			return s, ErrNoNode
		}
		return s, nil
	}

	// fix ups for special root node

	// lie about having the quota dir so stat on "/" xchks OK
	s.NumChildren++
	// Cversion for root begins at -1, then 0 on first child
	if len(cver.Kvs) == 0 {
		s.Cversion = -1
	}
	return s, nil
}
