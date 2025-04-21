When we spin up the worker on a Vast AI instance, we provide an authentication token that it will use to make requests to our server. The token is good for the duration of the instance lifetime and will be tied to the instance.

The token is provided to the worker via environment variable `WORKER_AUTH_TOKEN`

When the server receives an api request, it will check the auth header and check if the token exists and is still valid.

There are 2 tables involved in this check:

1. worker_auth_token_status
2. instance_status

For the token to work, the instance must be not be marked as `stopped` by the server and the token must be `created` when client connects for the first time and `validated` while the instance is still active. The server will mark the token as `invalidated` when the instance is stopped.

Token expiration is a server parameter that is used at runtime. The server will use this param and the earliest `created_at` time of the token to determine if the token can still be used.
