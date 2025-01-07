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
up:
	docker compose up -d --build

down:
	docker compose down

diagram.png: diagram.dot
	dot -Tpng "$<" > "$@"
