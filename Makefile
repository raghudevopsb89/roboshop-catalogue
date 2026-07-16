.PHONY: build run unit-test integration-test coverage docker-build db-init clean

build:
	go mod tidy && go build -o catalogue .

run:
	MYSQL_HOST=localhost MYSQL_USER=catalogue MYSQL_PASSWORD=RoboShop@1 MYSQL_DATABASE=catalogue go run .

unit-test:
	go test ./...

integration-test:
	go test -tags=integration ./...

coverage:
	go test -coverprofile=coverage.out -covermode=atomic ./...

docker-build:
	env
	docker build -t raghudevopsb89.azurecr.io/roboshop-catalogue:${GITHUB_SHA} .

docker-scan:
	trivy image raghudevopsb89.azurecr.io/roboshop-catalogue:${GITHUB_SHA} --exit-code 1 --ignore-unfixed -s HIGH,CRITICAL

docker-push:
	docker push raghudevopsb89.azurecr.io/roboshop-catalogue:${GITHUB_SHA}

db-init:
	mysql -h $${MYSQL_HOST:-localhost} -u root -pRoboShop@1 < db/app-user.sql
	mysql -h $${MYSQL_HOST:-localhost} -u root -pRoboShop@1 < db/schema.sql
	mysql -h $${MYSQL_HOST:-localhost} -u root -pRoboShop@1 < db/master-data.sql

clean:
	rm -f catalogue

sonar_token := $(shell az keyvault secret show --name sonarqube-token --vault-name roboshopb89 --query "value" -o tsv)

sonar-scan:
	echo /home/runner/sonar-scanner-7.1.0.4889-linux-x64/bin/sonar-scanner -D sonar.projectKey=roboshop-catalogue -Dsonar.host.url=http://10.1.0.46:9000 -Dsonar.token=$(sonar_token) -Dsonar.qualitygate.wait=true -D -Dsonar.go.coverage.reportPaths=coverage.out

