-- name: GetSigningKey :one
SELECT private_key_pem FROM auth_signing_keys WHERE id = 1;

-- name: InsertSigningKey :execrows
INSERT INTO auth_signing_keys (id, private_key_pem)
VALUES (1, $1)
ON CONFLICT (id) DO NOTHING;
