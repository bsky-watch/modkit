#!/bin/bash
set -e

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<-EOF
  CREATE USER labeler WITH PASSWORD '${MODKIT_LABELER_DB_PASSWORD}';
  CREATE DATABASE labels WITH OWNER = 'labeler';

  CREATE USER reports WITH PASSWORD '${MODKIT_REPORTS_DB_PASSWORD}';
  CREATE DATABASE reports WITH OWNER = 'reports';
EOF
