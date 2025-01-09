# Bluesky modkit

Moderation stack for Bluesky, based on [Redmine](https://www.redmine.org/).

> [!WARNING]
> It is in a very early stage of development, so there are still some sharp edges
> and missing functionality.
>
> Do not rush to replace your existing Ozone with it just yet. (Not in the least
> because it doesn't support labeling just yet.)
>
> Should be okay to use for setting up a new moderation account, but be prepared
> to do some troubleshooting when something breaks.

## Features

* Groups reports by subject account
* Manages moderation lists
* Stores snapshots of the reported content and profiles
* Moderators can also use in-app reports to add additional content to a ticket

### Planned future features

* Emitting labels for accounts and individual records
* Migration from Ozone

## Getting started

#### 1. Environment vars & overrides

Run `make .env` and edit `.env` file.

Copy `docker-compose.override.example.yml` to `docker-compose.override.yml` -
this will expose caddy on port 3000.

#### 2. Create initial configuration

Run `make gen-config`.

In `config/config.yaml` update:

  * `did` to the DID of your moderation account.
  * `password` to the password of the same account.
  * `publicHostname` to the hostname your instance will be reachable from outside world.

#### 3. Configure moderation account to act as a labelers

Run `go run ./cmd/account-setup`.

If your DID document needs to be updated, it will ask you to provide confirmaion token emailed to you. Re-run the same command with added `--token` argument to perform the change.

If no changes are needed, it will just tell you that everthing is OK.

#### 4. Start everything

Run `make up`.

#### 5. Log into Redmine

Default credentials are `admin`/`admin`.

Create a new account for yourself and add it to "Moderators" group. Update `config/mappings.yaml` with your username and DIDs of any accounts you plan to send in-app reports from. Run `docker compose restart redmine-handler report-processor` to pick up the changes.

#### 6. Extra auth for production

I'm not comfortable having Redmine publicly accessible, so for production setup
I recommend putting it behind an additional auth layer.

`docker-compose.yml` includes [Pomerium](https://www.pomerium.com/) service for
this purpose. To enable it:

1. Copy `files/pomerium.example.yaml` to `config/pomerium.yaml` and edit it.
2. Uncomment `COMPOSE_PROFILES="prod"` in `.env` file.

## System diagram

![](diagram.png)

## Monitoring

TODO
