#!/bin/sh

## A little helper script so I don't need to perform all these steps manually each time :)

docker exec electric-eel-build sudo rm -r /home/mersdk/app
./helper/make-app-bundle.sh
docker cp app electric-eel-build:/home/mersdk/app
docker exec -u root electric-eel-build chown -R mersdk:mersdk /home/mersdk/app
docker exec -w /home/mersdk/app electric-eel-build mb2 --target SailfishOS-5.2.0.15-aarch64 build
docker cp electric-eel-build:/home/mersdk/app/RPMS/harbour-electric-eel-0.2.5-1.aarch64.rpm app/RPMS/