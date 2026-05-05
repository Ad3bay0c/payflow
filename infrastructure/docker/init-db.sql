-- PayFlow database initialisation
-- Each service gets its own database -- this enforces the microservices
-- rule that no two services share a database.

CREATE DATABASE payflow_auth;
CREATE DATABASE payflow_payment;
CREATE DATABASE payflow_ledger;

-- Create dedicated low-privilege users per service
-- Each service only has access to its own database
CREATE USER payflow_auth    WITH PASSWORD 'auth_secret';
CREATE USER payflow_payment WITH PASSWORD 'payment_secret';
CREATE USER payflow_ledger  WITH PASSWORD 'ledger_secret';

-- Grant the payflow user access to all service databases
GRANT ALL PRIVILEGES ON DATABASE payflow_auth TO payflow_auth;
GRANT ALL PRIVILEGES ON DATABASE payflow_payment TO payflow_payment;
GRANT ALL PRIVILEGES ON DATABASE payflow_ledger TO payflow_ledger;

\c payflow_auth
GRANT ALL ON SCHEMA public TO payflow_auth;

\c payflow_payment
GRANT ALL ON SCHEMA public TO payflow_payment;

\c payflow_ledger
GRANT ALL ON SCHEMA public TO payflow_ledger;
