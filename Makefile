.PHONY: build run unit-test integration-test docker-build db-init clean

build:
	go mod tidy && go build -o catalogue .

run:
	MYSQL_HOST=localhost MYSQL_USER=catalogue MYSQL_PASSWORD=RoboShop@1 MYSQL_DATABASE=catalogue go run .

unit-test:
	go test ./...

integration-test:
	go test -tags=integration ./...

docker-build:
	env
	docker build -t raghudevopsb89.azurecr.io/roboshop-catalogue:${GITHUB_SHA} .

docker-push:
	docker push raghudevopsb89.azurecr.io/roboshop-catalogue:${GITHUB_SHA}

db-init:
	mysql -h $${MYSQL_HOST:-localhost} -u root -pRoboShop@1 < db/app-user.sql
	mysql -h $${MYSQL_HOST:-localhost} -u root -pRoboShop@1 < db/schema.sql
	mysql -h $${MYSQL_HOST:-localhost} -u root -pRoboShop@1 < db/master-data.sql

clean:
	rm -f catalogue
