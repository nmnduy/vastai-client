# Architecture

- postgres DB:
    - timescaledb extensions
    - tables
        - `instance_status` table
            - each record is the state of a vast AI node when we perform some action to it
            - `id` for the record id
            - `vast_ai_id` for the id of the vast ai node
            - `status` column: one of `rented`, `authenticated`, `running`, `stopped`
            - `created_at` column for the time the record was created. use timescaledb optimization with this column as ref.
        - `job_status` table
            - each record represents a state change of a job.
            - `id` for the record id.
            - `job_id` for the id of the job.
            - `status` column: one of `created`, `queued`, `running`, `completed`, `failed`.
            - `created_at` column for the time the record was created. use timescaledb optimization with this column as ref.
            - `error` column to store error messages if the job failed.
            - `result` column to store the result of the job if it completed successfully.
            - `instance_id` the id of the instance it ran on
            - `input` stores the input of the job
        - `worker_auth_token_status` table
            - `id` for the record id
            - `token_id` to group events that happen to the same token
            - `token` the unique auth token value generated for a worker instance
            - `instance_id` for the vast ai instance id the token is associated with
            - `status` column: one of `created`, `validated`, `expired`, `invalidated` # Describes the state change of the token
            - `created_at` column for the time the record was created. use timescaledb optimization with this column as ref.
- cmd/server
    - listen to requests and create records to the `job_status` table.
- cmd/worker
    - run on a vast.ai node
    - read from the `job_status` table and run the job
- clean up worker
    - archive records on `worker_auth_token_status` that is more than 1 month old
    - archive records on `instance_status` that is more than 1 year old
    - archive records on `job_status` that is more than 3 months old

When we start a Vast node we have to give it a one-time token that will allow it to call our API to get a list of jobs for it. The token will expire after 1 hour and it can't be used again. The token will be provided via the environment variable `WORKER_AUTH_TOKEN`

We use gRPC instead of HTTP for better performance. Also I should learn it at some point so the time is now.
