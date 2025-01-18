.PHONY: all
all: test diagram.png


.PHONY: test
test:
	go test -v ./...


.PHONY: build
build:
	docker compose build

.env:
	@cp example.env $@
	@echo "---------------------------------------------------------"
	@echo "Please edit the configuration variables in the .env file."
	@echo "---------------------------------------------------------"
	@exit 1

config:
	@mkdir $@

.PHONY: gen-config
gen-config: | .env config
	docker compose up --wait --build redmine
	docker compose exec redmine rake redmine:load_default_data REDMINE_LANG=en
	docker compose exec redmine rake modkit:print_mappings >> config/mappings.yaml
	docker compose exec redmine rake modkit:print_config >> config/config.yaml
	@echo
	@echo "---------------------------------------------------------"
	@echo "Please edit configuration files in config/ directory."
	@echo "---------------------------------------------------------"

.PHONY: up down
up: | .env
	@. ./.env && \
		( [ -d "$${DATA_DIR}/prometheus" ] || mkdir "$${DATA_DIR}/prometheus" ) && \
		( [ "$$(stat --format='%u' "$${DATA_DIR}/prometheus")" = "65534" ] || chown 65534:65534 "$${DATA_DIR}/prometheus" || ( echo "Please run chown 65534:65534 \"$${DATA_DIR}/prometheus\""; exit 1 ) )
	docker compose up -d --build

down:
	docker compose down

diagram.png: diagram.dot
	dot -Tpng "$<" > "$@"

# Only needed if you've initialized the DB before that script was added.
# Otherwise, postgres docker image would run it automatically on the first start.
.PHONY: create-extra-dbs
create-extra-dbs:
	docker compose exec db /docker-entrypoint-initdb.d/create-extra-databases.sh
