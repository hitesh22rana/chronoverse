-- No down migration: the cancellation of forged cross-tenant job rows is a
-- one-way data cleanup. The pre-cancellation state is not recoverable and
-- must not be reintroduced.
SELECT 1;
