package main

import (
	"crypto/x509"
	"flag"
	"fmt"
	"github.com/Rudro-25/extended-api-server/lib/certstore"
	"github.com/Rudro-25/extended-api-server/lib/server"
	"github.com/gorilla/mux"
	"github.com/spf13/afero"
	"k8s.io/client-go/util/cert"
	"log"
	"net"
	"net/http"
)

func main() {
	var proxy = false
	flag.BoolVar(&proxy, "receive-proxy-request", proxy, "proxy mode")
	flag.Parse()
	fs := afero.NewOsFs()
	store, err := certstore.NewCertStore(fs, certstore.CertDir)
	if err != nil {
		log.Fatal(err)
	}
	err = store.InitCA("database")
	if err != nil {
		log.Fatal(err)
	}
	serverCert, serverKey, err := store.NewServerCertPair(cert.AltNames{
		IPs: []net.IP{net.ParseIP("127.0.0.2")},
	})
	if err != nil {
		log.Fatal(err)
	}
	err = store.Write("tls", serverCert, serverKey)
	if err != nil {
		log.Fatal(err)
	}
	clientCert, clientKey, err := store.NewClientCertPair(cert.AltNames{
		DNSNames: []string{"deb"},
	})

	if err != nil {
		log.Fatal(err)
	}
	err = store.Write("deb", clientCert, clientKey)
	if err != nil {
		log.Fatal(err)
	}
	apiserverStore, err := certstore.NewCertStore(fs, certstore.CertDir)
	if err != nil {
		log.Fatal(err)
	}
	if proxy {
		err = apiserverStore.LoadCA("apiserver")
		if err != nil {
			log.Fatal(err)
		}
	}
	rhCACertPool := x509.NewCertPool()
	rhStore, err := certstore.NewCertStore(fs, certstore.CertDir)
	if err != nil {
		log.Fatal(err)
	}
	if proxy {
		err = rhStore.LoadCA("requestheader")
		if err != nil {
			log.Fatal(err)
		}
		rhCACertPool.AppendCertsFromPEM(rhStore.CACertBytes())
	}

	cfg := server.Config{
		Address:     "127.0.0.2:8443",
		CACertFiles: []string{},
		CertFile:    store.CertFile("tls"),
		KeyFile:     store.KeyFile("tls"),
	}

	if proxy {
		cfg.CACertFiles = append(cfg.CACertFiles, apiserverStore.CertFile("ca"))
		cfg.CACertFiles = append(cfg.CACertFiles, rhStore.CertFile("ca"))
	}

	srv := server.NewGenericServer(cfg)
	r := mux.NewRouter()
	r.HandleFunc("/database/{resource}", func(w http.ResponseWriter, r *http.Request) {
		user := "system:anonymous"
		src := "-"
		if len(r.TLS.PeerCertificates) > 0 {
			opts := x509.VerifyOptions{
				Roots:     rhCACertPool,
				KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
			}
			if _, err := r.TLS.PeerCertificates[0].Verify(opts); err != nil {
				user = r.TLS.PeerCertificates[0].Subject.CommonName
				src = "Client-Cert-CN"
			} else {
				user = r.Header.Get("X-Remote-User")
				src = "X-Remote-User"
			}
		}
		vars := mux.Vars(r)
		w.WriteHeader(http.StatusOK)
		fmt.Println("Resource: ", vars["resource"], " src: ", src, " user: ", user)

	})
	r.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok..")
	})
	srv.ListenAndServe(r)
}
