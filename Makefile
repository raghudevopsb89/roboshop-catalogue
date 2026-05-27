.PHONY: build run docker-build db-init clean

build:
	go mod tidy && go build -o catalogue .

run:
	MYSQL_HOST=localhost MYSQL_USER=catalogue MYSQL_PASSWORD=RoboShop@1 MYSQL_DATABASE=catalogue go run .

docker-build:
	docker build -t raghudevopsb89.azurecr.io/roboshop-catalogue .
	docker push raghudevopsb89.azurecr.io/roboshop-catalogue

db-init:
	mysql -h $${MYSQL_HOST:-localhost} -u root -pRoboShop@1 < db/app-user.sql
	mysql -h $${MYSQL_HOST:-localhost} -u root -pRoboShop@1 < db/schema.sql
	mysql -h $${MYSQL_HOST:-localhost} -u root -pRoboShop@1 < db/master-data.sql

clean:
	rm -f catalogue
