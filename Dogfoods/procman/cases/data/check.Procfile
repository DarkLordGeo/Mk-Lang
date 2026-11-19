# The services of a small web app
web [main limit=64k]: ./bin/server --port $PORT --name "my app"  # the front end
worker [restart=on-failure retries=5 backoff=250ms]: ./bin/worker --queue 'jobs high'
clock [restart=always backoff=2s]: ./bin/clock ${INTERVAL}s $$HOME
task migrate [limit=1m]: ./bin/migrate up
