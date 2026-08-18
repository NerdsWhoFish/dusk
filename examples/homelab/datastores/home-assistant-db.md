---
dusk: v1alpha1
kind: datastore
name: home-assistant-db
title: Home Assistant Database
relations:
  - type: runs_on
    to: host:home/mini
attributes:
  engine: PostgreSQL
  backup: nightly
---

Keeps long-lived Home Assistant history outside the application container.
