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
	"sync"

	"go.etcd.io/etcd/client/v3"
	"golang.org/x/net/context"
	"k8s.io/klog/v2"
)

type Session interface {
	Conn
	Watches
	Sid() Sid
	ZXid() ZXid
	ConnReq() ConnectRequest
	Backing() interface{}
}

type session struct {
	Conn
	*watches
	id  clientv3.LeaseID
	c   *clientv3.Client
	req ConnectRequest

	leaseZXid ZXid
	mu        sync.RWMutex
}

func (s *session) ConnReq() ConnectRequest { return s.req }
func (s *session) Backing() interface{}    { return s }

func newSession(c *clientv3.Client, zkc Conn, id clientv3.LeaseID) (*session, error) {
	ctx, cancel := context.WithCancel(c.Ctx())
	s := &session{Conn: zkc, id: id, c: c, watches: newWatches(c)}

	kach, kaerr := c.KeepAlive(ctx, id)
	if kaerr != nil {
		cancel()
		return nil, kaerr
	}

	go func() {
		klog.V(9).Infof("starting the session... id=%v", id)
		defer func() {
			klog.V(9).Infof("finishing the session... id=%v; expect revoke...", id)
			cancel()
			s.Close()
		}()
		for {
			select {
			case ka, ok := <-kach:
				if !ok {
					return
				}
				if ka.ResponseHeader == nil {
					continue
				}
				s.mu.Lock()
				s.leaseZXid = ZXid(ka.ResponseHeader.Revision)
				s.mu.Unlock()
			case <-s.StopNotify():
				return
			}
		}
	}()

	return s, nil
}

func (s *session) Sid() Sid { return Sid(s.id) }

func (s *session) Close() {
	s.watches.close()
	s.Conn.Close()
}

// ZXid gets the lease ZXid
func (s *session) ZXid() ZXid {
	s.mu.RLock()
	zxid := s.leaseZXid
	s.mu.RUnlock()
	return zxid
}
