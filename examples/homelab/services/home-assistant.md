---
dusk: v1alpha1
kind: service
name: home-assistant
title: Home Assistant
relations:
  - type: runs_on
    to: host:home/mini
  - type: depends_on
    to: datastore:home/home-assistant-db
attributes:
  url: https://home.example.com
  backup: nightly
---

Automates the house and is the source of truth for device state.
