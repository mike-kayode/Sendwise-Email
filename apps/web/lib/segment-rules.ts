export type SegmentField =
  | "email"
  | "status"
  | "tag"
  | "list"
  | "created_at"
  | `custom_fields.${string}`;

export type SegmentOperator =
  | "equals"
  | "not_equals"
  | "contains"
  | "does_not_contain"
  | "is_empty"
  | "is_not_empty"
  | "before"
  | "after";

export type SegmentCondition = {
  field: SegmentField;
  operator: SegmentOperator;
  value?: string;
};

export type SegmentGroup = {
  match: "AND" | "OR";
  conditions: SegmentCondition[];
};

export type SegmentRules = {
  groups: SegmentGroup[];
};
