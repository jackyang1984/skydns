// Copyright (c) 2014 The SkyDNS Authors. All rights reserved.
// Use of this source code is governed by The MIT License (MIT) that can be
// found in the LICENSE file.

package server

import (
	"log"
	"net"
	"strconv"
	"strings"

	"github.com/miekg/dns"
	"github.com/skynetservices/skydns/msg"
)

// Look in .../dns/stub/<domain>/xx for msg.Services. Loop through them
// extract <domain> and add them as forwarders (ip:port-combos) for
// the stubzones. Only numeric (i.e. IP address) hosts are used.
func (s *server) UpdateStubZones() {
	stubmap := make(map[string][]string)

	services, err := s.backend.Records("stub.dns."+s.config.Domain, false)
	if err != nil {
		log.Printf("skydns: stubzone update failed: %s", err)
		return
	}
	for _, serv := range services {
		if serv.Port == 0 {
			serv.Port = 53
		}
		ip := net.ParseIP(serv.Host)
		if ip == nil {
			log.Printf("skydns: stubzone non-address %s seen for: %s", serv.Key, serv.Host)
			continue
		}

		domain := msg.Domain(serv.Key)
		// Chop of left most label, because that is used as the nameserver place holder
		// and drop the right most labels that belong to localDomain.
		labels := dns.SplitDomainName(domain)
		domain = dns.Fqdn(strings.Join(labels[1:len(labels)-dns.CountLabel(s.config.localDomain)], "."))

		// if domain if the remaining name equals s.config.LocalDomain we ignore it.
		if domain == s.config.localDomain {
			log.Printf("skydns: not adding stub zone for my own domain")
			continue
		}
		stubmap[domain] = append(stubmap[domain], net.JoinHostPort(serv.Host, strconv.Itoa(serv.Port)))
	}

	s.config.stub = &stubmap
}

// ServeDNSForward forwards a request to a nameservers and returns the response.
func (s *server) ServeDNSStubForward(w dns.ResponseWriter, req *dns.Msg, ns []string) {
	StatsStubForwardCount.Inc(1)

	// Very similar to ServeDNSForward. Maybe refactor them both.

	tcp := false
	if _, ok := w.RemoteAddr().(*net.TCPAddr); ok {
		tcp = true
	}

	var (
		r   *dns.Msg
		err error
		try int
	)
	// Use request Id for "random" nameserver selection.
	nsid := int(req.Id) % len(ns)
Redo:
	switch tcp {
	case false:
		r, _, err = s.dnsUDPclient.Exchange(req, ns[nsid])
	case true:
		r, _, err = s.dnsTCPclient.Exchange(req, ns[nsid])
	}
	if err == nil {
		r.Compress = true
		r.Id = req.Id
		w.WriteMsg(r)
		return
	}
	// Seen an error, this can only mean, "server not reached", try again
	// but only if we have not exausted our nameservers.
	if try < len(ns) {
		try++
		nsid = (nsid + 1) % len(ns)
		goto Redo
	}

	log.Printf("skydns: failure to forward stub request %q", err)
	m := new(dns.Msg)
	m.SetReply(req)
	m.SetRcode(req, dns.RcodeServerFailure)
	w.WriteMsg(m)
}
