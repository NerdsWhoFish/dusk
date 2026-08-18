---
dusk: v1alpha1
kind: service
name: reverse-proxy
title: Reverse Proxy
relations:
  - type: runs_on
    to: host:home/mini
  - type: exposes
    to: service:home/home-assistant
attributes:
  url: https://proxy.example.com
---

Terminates TLS and routes private service hostnames.
