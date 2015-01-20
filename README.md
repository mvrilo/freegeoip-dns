# freegeoip-dns

[freegeoip](https://github.com/fiorix/freegeoip) over DNS TXT records.

This is a customized version of the [freegeoip tool](https://github.com/fiorix/freegeoip/blob/2a71e974d51c5045f7c523979a5dd306a714e1f4/cmd/freegeoip/main.go) slightly modified to work as a DNS server.

# EXAMPLE

```
# ./freegeoip-dns
dig @127.0.0.1 -p5300 google.com txt +short
"2800:3f0:4003:c00::8b    AR    Argentina                -34.00    -64.00    0"
dig @127.0.0.1 -p5300 murilo.in txt +short
"23.23.109.126    US        VA            20146    America/New_York    39.04    -77.49    511"
dig @127.0.0.1 -p5300 github.com txt +short
"192.30.252.129    US    United States    CA    California    San Francisco    94107    America/Los_Angeles    37.77    -122.39    807"
```

# NOTES

The server can be used to query for IP addresses too, you'll need to pass a domain argument to treat the IP as subdomain. Example follows:

```
# ./freegeoip-dns -domain=freegeoip
dig @127.0.0.1 -p5300 192.30.252.129.freegeoip txt +short
"192.30.252.129    US    United States    CA    California    San Francisco    94107    America/Los_Angeles    37.77    -122.39    807"
```

# INSTALLATION

```
go get github.com/mvrilo/freegeoip-dns
```

# AUTHOR

Murilo Santana <<mvrilo@gmail.com>>

# CREDIT

The freegeoip authors.
