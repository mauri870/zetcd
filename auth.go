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

type AuthFunc func(AuthConn) (Session, error)
type ZKFunc func(Session) (ZK, error)

func NewAuth(c *clientv3.Client) AuthFunc {
	sp := NewSessionPool(c)
	return func(zka AuthConn) (Session, error) {
		return sp.Auth(zka)
	}
}

func NewZK(c *clientv3.Client) ZKFunc {
	return func(s Session) (ZK, error) {
		return NewZKLog(NewZKEtcd(c, s)), nil
	}
}
