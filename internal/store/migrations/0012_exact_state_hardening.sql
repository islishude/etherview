-- P20-T09 makes an exact NFT observation write-once for its immutable block
-- identity. Application upserts may execute a no-op update for an identical
-- concurrent observation, but no persisted field may change afterward.

CREATE OR REPLACE FUNCTION reject_exact_nft_observation_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW IS DISTINCT FROM OLD THEN
        RAISE EXCEPTION 'exact NFT state observations are immutable'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS erc721_owner_reconciliations_immutable_trigger
    ON erc721_owner_reconciliations;
CREATE TRIGGER erc721_owner_reconciliations_immutable_trigger
BEFORE UPDATE ON erc721_owner_reconciliations
FOR EACH ROW EXECUTE FUNCTION reject_exact_nft_observation_mutation();

DROP TRIGGER IF EXISTS erc1155_balance_reconciliations_immutable_trigger
    ON erc1155_balance_reconciliations;
CREATE TRIGGER erc1155_balance_reconciliations_immutable_trigger
BEFORE UPDATE ON erc1155_balance_reconciliations
FOR EACH ROW EXECUTE FUNCTION reject_exact_nft_observation_mutation();
