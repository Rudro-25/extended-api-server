package main

import (
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"github.com/Rudro-25/extended-api-server/lib/certstore"
	"github.com/Rudro-25/extended-api-server/lib/server"
	"github.com/gorilla/mux"
	"github.com/spf13/afero"
	"io"
	"k8s.io/client-go/util/cert"
	"log"
	"net"
	"net/http"
	"time"
)

func main() {
	// proxy is used to determine is this server used as a proxy
	// to another server
	var proxy = false
	flag.BoolVar(&proxy, "send-proxy-request", proxy, "forward requests to database extended apiServer")
	flag.Parse()

	fs := afero.NewOsFs()
	store, err := certstore.NewCertStore(fs, certstore.CertDir)
	if err != nil {
		log.Fatal(err)
	}
	err = store.InitCA("apiserver")
	if err != nil {
		log.Fatal(err)
	}
	serverCert, serverKey, err := store.NewServerCertPair(cert.AltNames{
		IPs: []net.IP{net.ParseIP("127.0.0.1")},
	})

	if err != nil {
		log.Fatalln(err)
	}

	err = store.Write("tls", serverCert, serverKey)
	if err != nil {
		log.Fatalln(err)
	}
	clientCert, clientKey, err := store.NewClientCertPair(cert.AltNames{
		DNSNames: []string{"rudro"},
	})
	if err != nil {
		log.Fatalln(err)
	}
	err = store.Write("rudro", clientCert, clientKey)
	if err != nil {
		log.Fatal(err)
	}

	// Another cert and key for making request to eas from as.
	// Because a client is making request to eas through as.
	rhStore, err := certstore.NewCertStore(fs, certstore.CertDir)
	if err != nil {
		log.Fatal(err)
	}
	err = rhStore.InitCA("requestheader")
	if err != nil {
		log.Fatal(err)
	}
	rhClientCert, rhClientKey, err := store.NewClientCertPair(cert.AltNames{
		DNSNames: []string{"apiserver.rudro"}, // because apiserver is making the calls to database eas
	})
	if err != nil {
		log.Fatal(err)
	}
	err = rhStore.Write("apiserver.rudro", rhClientCert, rhClientKey)
	if err != nil {
		log.Fatal(err)
	}
	rhCert, err := tls.LoadX509KeyPair(rhStore.CertFile("apiserver.rudro"), rhStore.KeyFile("apiserver.rudro"))
	if err != nil {
		log.Fatal(err)
	}

	// This certpool will be used to verify eas certificates. It holds CA of server
	easCACertPool := x509.NewCertPool()
	if proxy {
		easStore, err := certstore.NewCertStore(fs, certstore.CertDir)

		if err != nil {
			log.Fatal(err)
		}

		err = easStore.LoadCA("database")
		if err != nil {
			log.Fatal(err)
		}
		easCACertPool.AppendCertsFromPEM(easStore.CACertBytes())
	}
	cfg := server.Config{
		Address: "127.0.0.1:8443",
		CACertFiles: []string{
			store.CertFile("ca"),
		},
		CertFile: store.CertFile("tls"),
		KeyFile:  store.KeyFile("tls"),
	}
	srv := server.NewGenericServer(cfg)
	r := mux.NewRouter()
	r.HandleFunc("/core/{resource}", func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "resource: %v\n", vars["resource"])
	})

	// HandleFunc for proxying the request to eas
	if proxy {
		fmt.Println("...FORWARDING.....")
		r.HandleFunc("/database/{resource}", func(w http.ResponseWriter, r *http.Request) {
			tr := &http.Transport{
				MaxConnsPerHost: 10,
				TLSClientConfig: &tls.Config{
					Certificates: []tls.Certificate{rhCert},
					RootCAs:      easCACertPool,
					// Why this CA necessary?
					// When apiserver is trying to connect database server, then api server
					// need to verify that the certificate that is database server providing is correct.
					// this verification is done
				},
			}
			client := http.Client{
				Transport: tr,
				Timeout:   20 * time.Second,
			}
			u := *r.URL
			u.Scheme = "https"

			// database api add
			u.Host = "127.0.0.2:8443"
			fmt.Printf("Forwarding request to %s\n", u.String())
			req, _ := http.NewRequest(r.Method, u.String(), nil)

			// If external client exist or this server is a proxy server. Setting that external client
			// profile to auth from eas
			if len(r.TLS.PeerCertificates) > 0 {
				req.Header.Set("X-Remote-User", r.TLS.PeerCertificates[0].Subject.CommonName)
			}
			res, err := client.Do(req)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				log.Println(err)
				return
			}
			defer res.Body.Close()
			w.WriteHeader(http.StatusOK)
			io.Copy(w, res.Body)
		})
	}

	r.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "OK")
	})
	srv.ListenAndServe(r)
}
