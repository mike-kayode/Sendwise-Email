UPDATE segments
SET rules = jsonb_build_object(
    'groups',
    jsonb_build_array(
        jsonb_build_object(
            'match',
            CASE WHEN lower(rules->>'match') = 'any' THEN 'OR' ELSE 'AND' END,
            'conditions',
            rules->'conditions'
        )
    )
)
WHERE jsonb_typeof(rules->'conditions') = 'array'
  AND CASE
      WHEN jsonb_typeof(rules->'conditions') = 'array'
      THEN jsonb_array_length(rules->'conditions') > 0
      ELSE false
  END;

ALTER TABLE segments
    ALTER COLUMN rules SET DEFAULT '{"groups":[]}'::jsonb;
