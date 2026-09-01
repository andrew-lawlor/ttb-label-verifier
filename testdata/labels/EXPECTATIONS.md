# Test Label Expectations

Synthetic labels rendered with PIL (not an external image generator) so the ground-truth text is known exactly. Each case targets a specific rule or edge case from the take-home's interview notes. `manifest.csv` in this directory has the application-side data to submit alongside each image via the batch upload page.

## 01_perfect_match_bourbon.jpg

ALL PASS — baseline case, everything matches exactly.

## 02_brand_casing_variant.jpg

ALL PASS — brand is all-caps on label vs title case on application; fuzzy match should treat this as the same brand (Dave's interview example).

## 03_warning_titlecase_fail.jpg

government_warning FAILS (label prints it in Title Case, not ALL CAPS) -> overall FAIL, even though every other field matches. Jenny's interview example.

## 04_abv_mismatch.jpg

alcohol_content FAILS — label reads 40%%, application says 45%% (5 points, outside the +/-0.5 tolerance) -> overall FAIL.

## 05_proof_notation_match.jpg

ALL PASS — label only prints '90 Proof', application states '45%%'; these are numerically the same (proof / 2 = ABV%%).

## 06_net_contents_unit_variant.jpg

ALL PASS — label prints '1 L', application says '1000 mL'; same volume, different units.

## 07_net_contents_mismatch.jpg

net_contents FAILS — label reads 375 mL (a half bottle), application says 750 mL -> overall FAIL.

## 08_brand_major_mismatch.jpg

brand_name FAILS — completely different brand text (simulates the wrong label attached to an application) -> overall FAIL.

## 09_low_quality_blurry.jpg

Best-effort case (Jenny's 'photographed at weird angles / bad lighting' example) — content matches if legible; exercises the low-confidence -> needs_review path if the model can't read it cleanly. Not a strict pass/fail expectation.

## 10_wine_full_match.jpg

ALL PASS — wine label, full match, covers a different beverage type than the bourbon baseline.

## 11_beer_full_match.jpg

ALL PASS — beer label, full match, covers the third beverage type (beer/wine/spirits).

