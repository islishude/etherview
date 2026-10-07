ALTER TABLE blocks
    ADD COLUMN slot_number_raw JSONB
        GENERATED ALWAYS AS (raw->'slotNumber') STORED;
