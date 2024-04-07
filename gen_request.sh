#!/bin/bash

url="http://localhost:8080/users"

while true; do
    # Generate random user data
    username=$(cat /dev/urandom | tr -dc 'a-zA-Z0-9' | fold -w 10 | head -n 1)
    address=$(cat /dev/urandom | tr -dc 'a-zA-Z0-9' | fold -w 10 | head -n 1)
    email="$username@example.com"

    # Create JSON request
    json=$(jq -n \
        --arg un "$username" \
        --arg em "$email" \
        --arg ad "$address" \
        '{name: $un, email: $em, address: $ad, phone: "00000"}')

    # Send POST request
    curl -s -X POST -H "Content-Type: application/json" -d "$json" $url

    # Sleep for a while before the next request
    sleep 1
done

wait
