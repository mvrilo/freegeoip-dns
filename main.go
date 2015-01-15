package main

import (
	"flag"
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

func response(query *Query, ip net.IP, lang string) []string {
	record := struct {
		IP          string  `json:"ip"`
		CountryCode string  `json:"country_code"`
		CountryName string  `json:"country_name"`
		RegionCode  string  `json:"region_code"`
		RegionName  string  `json:"region_name"`
		City        string  `json:"city"`
		ZipCode     string  `json:"zip_code"`
		TimeZone    string  `json:"time_zone"`
		Latitude    float64 `json:"latitude"`
		Longitude   float64 `json:"longitude"`
		MetroCode   uint    `json:"metro_code"`
	}{
		IP:          ip.String(),
		CountryCode: query.Country.ISOCode,
		CountryName: query.Country.Names[lang],
		City:        query.City.Names[lang],
		ZipCode:     query.Postal.Code,
		TimeZone:    query.Location.TimeZone,
		Latitude:    roundFloat(query.Location.Latitude, .5, 3),
		Longitude:   roundFloat(query.Location.Longitude, .5, 3),
		MetroCode:   query.Location.MetroCode,
	}
	if len(query.Region) > 0 {
		record.RegionCode = query.Region[0].ISOCode
		record.RegionName = query.Region[0].Names[lang]
	}

	return []string{
		ip.String(),
		record.CountryCode,
		record.CountryName,
		record.RegionCode,
		record.RegionName,
		record.City,
		record.ZipCode,
		record.TimeZone,
		strconv.FormatFloat(record.Latitude, 'f', 2, 64),
		strconv.FormatFloat(record.Longitude, 'f', 2, 64),
		strconv.Itoa(int(record.MetroCode)),
	}
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

func main() {
	addr := flag.String("addr", ":5300", "Address in form of ip:port to listen on")
	suffix := flag.String("suffix", "", "Domain suffix for the DNS queries")
	ipdb := flag.String("db", maxmindFile, "IP database file or URL")
	updateIntvl := flag.Duration("update", 24*time.Hour, "Database update check interval")
	retryIntvl := flag.Duration("retry", time.Hour, "Max time to wait before retrying update")
	silent := flag.Bool("silent", false, "Do not log requests to stderr")
	lang := flag.String("lang", "en", "Language to return the fields, e.g. country name")
	// redisAddr := flag.String("redis", "127.0.0.1:6379", "Redis address in form of ip:port for quota")
	// quotaMax := flag.Int("quota-max", 0, "Max requests per source IP per interval; Set 0 to turn off")
	// quotaIntvl := flag.Duration("quota-interval", time.Hour, "Quota expiration interval")
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
	dns.HandleFunc(*suffix+".", func(w dns.ResponseWriter, r *dns.Msg) {
		q := r.Question[0]
		log.Println(q.String())
		if q.Qtype == dns.TypeTXT && q.Qclass == dns.ClassINET {
			ip := queryIP(q, *suffix)

			m := new(dns.Msg)
			m.SetReply(r)

			var query Query
			db.Lookup(ip, &query)

			txt := new(dns.TXT)
			txt.Hdr = dns.RR_Header{Name: q.Name, Rrtype: dns.TypeTXT, Class: dns.ClassINET, Ttl: 0}
			txt.Txt = response(&query, ip, *lang)

			m.Answer = append(m.Answer, txt)
			w.WriteMsg(m)
		}
	})

	if !*silent {
		log.Println("freegeoip dns server starting on", *addr)
		go logEvents(db)
	}
	panic(server.ListenAndServe())
}

func queryIP(q dns.Question, suffix string) net.IP {
	h := strings.Split(q.Name, "."+suffix)[0]
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
