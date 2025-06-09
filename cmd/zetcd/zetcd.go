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

package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"net"
	"net/http"
	netpprof "net/http/pprof"
	"net/url"
	"os"
	"os/signal"
	"runtime/pprof"
	"strings"
	"time"

	"github.com/mauri870/zetcd"
	"github.com/mauri870/zetcd/version"
	"github.com/mauri870/zetcd/xchk"
	"github.com/mauri870/zetcd/zk"
	"go.etcd.io/etcd/client/pkg/v3/transport"
	clientv3 "go.etcd.io/etcd/client/v3"
	etcdembed "go.etcd.io/etcd/server/v3/embed"
	"golang.org/x/net/context"
)

type personality struct {
	authf zetcd.AuthFunc
	zkf   zetcd.ZKFunc
	ctx   context.Context
}

func getTlsConfig(etcdCertFile string, etcdKeyFile string, etcdCaFile string) (*tls.Config, error) {
	tlsInfo := transport.TLSInfo{
		CertFile:      etcdCertFile,
		KeyFile:       etcdKeyFile,
		TrustedCAFile: etcdCaFile,
	}
	tlsConfig, err := tlsInfo.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("ERROR: %s", err)
	}
	return tlsConfig, nil
}

func newZKSecureEtcd(ctx context.Context, etcdEps []string, tlsConfig *tls.Config) (p personality) {
	// talk to the etcd3 server
	c, err := clientv3.New(clientv3.Config{
		Endpoints: etcdEps,
		TLS:       tlsConfig,
		Context:   ctx,
	})
	if err != nil {
		panic(err)
	}
	p.authf = zetcd.NewAuth(c)
	p.zkf = zetcd.NewZK(c)
	p.ctx = c.Ctx()
	return p
}

func newZKEtcd(ctx context.Context, etcdEps []string) (p personality) {
	// talk to the etcd3 server
	c, err := clientv3.New(clientv3.Config{
		Endpoints: etcdEps,
		Context:   ctx,
	})
	if err != nil {
		panic(err)
	}
	p.authf = zetcd.NewAuth(c)
	p.zkf = zetcd.NewZK(c)
	p.ctx = c.Ctx()
	return p
}

func newBridge(ctx context.Context, bridgeAddr string) (p personality) {
	// proxy to zk server
	p.authf = zk.NewAuth([]string{bridgeAddr})
	p.zkf = zk.NewZK()
	p.ctx = ctx
	return p
}

func newOracle(ctx context.Context, etcdEps []string, bridgeAddr, oracle string) (p personality) {
	var cper, oper personality
	switch oracle {
	case "zk":
		cper, oper = newZKEtcd(ctx, etcdEps), newBridge(ctx, bridgeAddr)
	case "etcd":
		oper, cper = newZKEtcd(ctx, etcdEps), newBridge(ctx, bridgeAddr)
	default:
		fmt.Println("oracle expected etcd or zk, got", oracle)
		os.Exit(1)
	}
	p.authf = xchk.NewAuth(cper.authf, oper.authf, nil)
	p.zkf = xchk.NewZK(cper.zkf, oper.zkf, nil)
	p.ctx = cper.ctx
	return p
}

func main() {
	etcdAddrs := flag.String("endpoints", "", "etcd3 client address")
	pprofAddr := flag.String("pprof-addr", "", "enable pprof with a listen address")
	cpuprofile := flag.String("cpuprofile", "", "write cpu profile to file")
	etcdCertFile := flag.String("certfile", "", "etcd3 cert file")
	etcdKeyFile := flag.String("keyfile", "", "etcd3 key file")
	etcdCaFile := flag.String("cafile", "", "etcd3 ca file")
	zkaddr := flag.String("zkaddr", "", "address for serving zookeeper clients")
	oracle := flag.String("debug-oracle", "", "oracle zookeeper server address")
	bridgeAddr := flag.String("debug-zkbridge", "", "bridge zookeeper server address")
	embeddedEtcd := flag.Bool("embedded-etcd", false, "use embedded etcd server instead of connecting to an external one")

	flag.Parse()
	fmt.Println("Running zetcd proxy")
	fmt.Println("Version:", version.Version)
	fmt.Println("SHA:", version.SHA)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, os.Kill)
	defer cancel()

	if len(*zkaddr) == 0 {
		fmt.Println("expected -zkaddr")
		os.Exit(1)
	}

	if *cpuprofile != "" {
		f, err := os.Create(*cpuprofile)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()
	}

	if len(*pprofAddr) != 0 {
		httpmux := http.NewServeMux()
		pfx := "/debug/pprof/"
		httpmux.Handle(pfx, http.HandlerFunc(netpprof.Index))
		httpmux.Handle(pfx+"profile", http.HandlerFunc(netpprof.Profile))
		httpmux.Handle(pfx+"symbol", http.HandlerFunc(netpprof.Index))
		httpmux.Handle(pfx+"cmdline", http.HandlerFunc(netpprof.Cmdline))
		httpmux.Handle(pfx+"trace", http.HandlerFunc(netpprof.Trace))
		for _, s := range []string{"heap", "goroutine", "threadcreate", "block"} {
			httpmux.Handle(pfx+s, netpprof.Handler(s))
		}
		pprofListener, err := net.Listen("tcp", *pprofAddr)
		if err != nil {
			fmt.Printf("failed to listen on pprof address %q (%v)\n", *pprofAddr, err)
		}
		pprofServer := &http.Server{Handler: httpmux}
		//nolint:errcheck
		go pprofServer.Serve(pprofListener)
	}

	// listen on zookeeper server port
	ln, err := net.Listen("tcp", *zkaddr)
	if err != nil {
		os.Exit(1)
	}

	var p personality
	serv := zetcd.Serve
	etcdEps := strings.Split(*etcdAddrs, ",")

	if *embeddedEtcd {
		if len(etcdEps) != 1 {
			fmt.Println("expected -endpoints to have one endpoint for embedded etcd")
			os.Exit(1)
		}
		fmt.Println("Starting embedded etcd server")
		proto := "http"
		if len(*etcdCertFile) != 0 {
			proto = "https"
		}
		ep := etcdEps[0]
		clientURL, err := url.Parse(fmt.Sprintf("%s://%s", proto, ep))
		if err != nil {
			fmt.Printf("failed to parse etcd endpoint %q (%v)\n", ep, err)
			os.Exit(1)
		}
		e, err := startEmbeddedEtcdServer(clientURL)
		if err != nil {
			fmt.Printf("failed to start embedded etcd server (%v)\n", err)
			os.Exit(1)
		}
		//nolint:errcheck
		defer e.Close()
		select {
		case <-e.Server.ReadyNotify():
			fmt.Println("Server is ready!")
		case <-time.After(60 * time.Second):
			e.Server.Stop()
			fmt.Printf("Server took too long to start!")
			os.Exit(1)
		}
		fmt.Printf("Embedded etcd server started at %s\n", *etcdAddrs)
	}

	switch {
	case *oracle != "":
		if len(*etcdAddrs) == 0 || len(*bridgeAddr) == 0 {
			fmt.Println("expected -endpoints and -zkbridge")
			os.Exit(1)
		}
		p = newOracle(ctx, etcdEps, *bridgeAddr, *oracle)
		serv = zetcd.ServeSerial
	case len(*etcdAddrs) != 0 && len(*bridgeAddr) != 0:
		fmt.Println("expected -endpoints or -zkbridge but not both")
		os.Exit(1)
	case len(*etcdAddrs) != 0:
		if len(*etcdCertFile) != 0 && len(*etcdKeyFile) != 0 && len(*etcdCaFile) != 0 {
			tlsConfig, _ := getTlsConfig(*etcdCertFile, *etcdKeyFile, *etcdCaFile)
			p = newZKSecureEtcd(ctx, etcdEps, tlsConfig)
		} else {
			p = newZKEtcd(ctx, etcdEps)
		}
	case len(*bridgeAddr) != 0:
		p = newBridge(ctx, *bridgeAddr)
	default:
		fmt.Println("expected -endpoints or -zkbridge")
		os.Exit(1)
	}

	fmt.Println("Listening on", *zkaddr)
	serv(p.ctx, ln, p.authf, p.zkf)
}

func startEmbeddedEtcdServer(clientURL *url.URL) (*etcdembed.Etcd, error) {
	cfg := etcdembed.NewConfig()
	cfg.ListenClientUrls = []url.URL{*clientURL}
	cfg.Dir = "default.etcd"
	e, err := etcdembed.StartEtcd(cfg)
	if err != nil {
		return nil, err
	}
	return e, nil
}
