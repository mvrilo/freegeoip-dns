// Copyright 2015 Murilo Santana <mvrilo@gmail.com> and the freegeoip authors.
// All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package main

import (
	"flag"
	"fmt"
	"log"
	"math"
	"math/rand"
	"net"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/fiorix/freegeoip"
	"github.com/miekg/dns"
)

const (
	VERSION     = "0.0.1"
	maxmindFile = "http://geolite.maxmind.com/download/geoip/database/GeoLite2-City.mmdb.gz"
)

// maxmindQuery is the object used to query the maxmind database.
type Query struct {
	Country struct {
		ISOCode string            `maxminddb:"iso_code"`
		Names   map[string]string `maxminddb:"names"`
	} `maxminddb:"country"`
	Region []struct {
		ISOCode string            `maxminddb:"iso_code"`
		Names   map[string]string `maxminddb:"names"`
	} `maxminddb:"subdivisions"`
	City struct {
		Names map[string]string `maxminddb:"names"`
	} `maxminddb:"city"`
	Location struct {
		Latitude  float64 `maxminddb:"latitude"`
		Longitude float64 `maxminddb:"longitude"`
		MetroCode uint    `maxminddb:"metro_code"`
		TimeZone  string  `maxminddb:"time_zone"`
	} `maxminddb:"location"`
	Postal struct {
		Code string `maxminddb:"code"`
	} `maxminddb:"postal"`
}

func roundFloat(val float64, roundOn float64, places int) (newVal float64) {
	var round float64
	pow := math.Pow(10, float64(places))
	digit := pow * val
	_, div := math.Modf(digit)
	if div >= roundOn {
		round = math.Ceil(digit)
	} else {
		round = math.Floor(digit)
	}
	return round / pow
}

func response(query *Query, ip net.IP, lang string) string {
	ret := []string{
		ip.String(),
		query.Country.ISOCode,
		query.Country.Names[lang],
	}

	if len(query.Region) > 0 {
		ret = append(ret, []string{
			query.Region[0].ISOCode,
			query.Region[0].Names[lang],
		}...)
	}

	ret = append(ret, []string{
		query.City.Names[lang],
		query.Postal.Code,
		query.Location.TimeZone,
		strconv.FormatFloat(query.Location.Latitude, 'f', 2, 64),
		strconv.FormatFloat(query.Location.Longitude, 'f', 2, 64),
		strconv.Itoa(int(query.Location.MetroCode)),
	}...)

	return strings.Join(ret, "    ")
}

// openDB opens and returns the IP database.
func openDB(dsn string, updateIntvl, maxRetryIntvl time.Duration) (db *freegeoip.DB, err error) {
	u, err := url.Parse(dsn)
	if err != nil || len(u.Scheme) == 0 {
		db, err = freegeoip.Open(dsn)
	} else {
		db, err = freegeoip.OpenURL(dsn, updateIntvl, maxRetryIntvl)
	}
	return
}

type handle struct {
	db     *freegeoip.DB
	silent bool
	lang   string
	domain string
}

func (h *handle) log(resolved bool, start time.Time, w dns.ResponseWriter, r *dns.Msg) {
	if h.silent {
		return
	}

	q := r.Question[0]
	info := fmt.Sprintf("Question: Type=%s Class=%s Name=%s", dns.TypeToString[q.Qtype], dns.ClassToString[q.Qclass], q.Name)

	durr := time.Now().Sub(start)
	if resolved {
		log.Printf("%s (RESOLVED) %s\n", info, durr)
		return
	}

	log.Printf("%s (NXDOMAIN) %s\n", info, durr)
}

func (h *handle) nxdomain(start time.Time, w dns.ResponseWriter, r *dns.Msg) {
	m := new(dns.Msg)
	m.SetReply(r)
	m.Rcode = dns.RcodeNameError
	w.WriteMsg(m)
	h.log(false, start, w, r)
}

func (h *handle) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	start := time.Now()
	q := r.Question[0]
	if q.Qtype == dns.TypeTXT && q.Qclass == dns.ClassINET {
		ip := queryIP(q, h.domain)
		if ip == nil {
			h.nxdomain(start, w, r)
			return
		}

		m := new(dns.Msg)
		m.SetReply(r)

		var query Query
		h.db.Lookup(ip, &query)

		txt := new(dns.TXT)
		txt.Hdr = dns.RR_Header{Name: q.Name, Rrtype: dns.TypeTXT, Class: dns.ClassINET, Ttl: 0}
		txt.Txt = []string{response(&query, ip, h.lang)}

		m.Answer = append(m.Answer, txt)
		w.WriteMsg(m)
		h.log(true, start, w, r)
	} else {
		h.nxdomain(start, w, r)
	}
}

func main() {
	addr := flag.String("addr", ":5300", "Address in form of ip:port to listen on")
	domain := flag.String("domain", "", "Domain for the DNS queries")
	ipdb := flag.String("db", maxmindFile, "IP database file or URL")
	updateIntvl := flag.Duration("update", 24*time.Hour, "Database update check interval")
	retryIntvl := flag.Duration("retry", time.Hour, "Max time to wait before retrying update")
	silent := flag.Bool("silent", false, "Do not log requests to stderr")
	lang := flag.String("lang", "en", "Language to return the fields, e.g. country name")
	version := flag.Bool("version", false, "Show version and exit")
	flag.Parse()

	if *version {
		log.Printf("freegeoip v%s\n", VERSION)
		return
	}

	db, err := openDB(*ipdb, *updateIntvl, *retryIntvl)
	if err != nil {
		log.Fatal(err)
	}

	runtime.GOMAXPROCS(runtime.NumCPU())

	server := &dns.Server{Addr: *addr, Net: "udp"}
	dns.Handle(*domain+".", &handle{db, *silent, *lang, *domain})

	if !*silent {
		log.Println("freegeoip dns server starting on", *addr)
		go logEvents(db)
	}
	log.Fatal(server.ListenAndServe())
}

func queryIP(q dns.Question, domain string) net.IP {
	h := q.Name
	if domain != "" {
		h = strings.Split(q.Name, "."+domain)[0]
	}
	if ip := net.ParseIP(h); ip != nil {
		return ip
	}
	ip, err := net.LookupIP(h)
	if err != nil {
		return nil // Not found.
	}
	if len(ip) == 0 {
		return nil
	}
	return ip[rand.Intn(len(ip))]
}

// logEvents logs database events.
func logEvents(db *freegeoip.DB) {
	for {
		select {
		case file := <-db.NotifyOpen():
			log.Println("database loaded:", file)
		case err := <-db.NotifyError():
			log.Println("database error:", err)
		case <-db.NotifyClose():
			return
		}
	}
}
