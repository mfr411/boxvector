

## Web UI

```
boxvector web passwd admin                                 # create a user (/etc/boxvector/web.users, mode 600)
boxvector web -c /etc/boxvector.yml --listen 127.0.0.1:8180 # serve behind a reverse proxy, or add --tls-cert/--tls-key
```

Peers (live BIRD state), add peer, config editor with diff + dry-run before apply,
automatic snapshot and rollback, route lookup, apply history. Login is required;
plain HTTP is refused on non-loopback addresses.
