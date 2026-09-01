#!/usr/bin/env python3
"""Generates synthetic test label images + a matching manifest.csv for the
TTB label verification prototype. Rendered with PIL (not an external image
generator) so ground-truth text is exact and known, letting each test case
target a specific edge case from the take-home's interview notes.
"""
import csv
import os
from PIL import Image, ImageDraw, ImageFont, ImageFilter

SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))
OUT_DIR = os.path.join(SCRIPT_DIR, "labels")
os.makedirs(OUT_DIR, exist_ok=True)

FONT_CANDIDATES = [
    "/usr/share/fonts/truetype/dejavu",  # Debian/Ubuntu
    "/usr/share/fonts/dejavu",  # Fedora/RHEL
    "/opt/homebrew/share/fonts",  # macOS (Homebrew)
]
FONT_DIR = next((d for d in FONT_CANDIDATES if os.path.isdir(d)), FONT_CANDIDATES[0])

SERIF_BOLD = os.path.join(FONT_DIR, "DejaVuSerif-Bold.ttf")
SERIF = os.path.join(FONT_DIR, "DejaVuSerif.ttf")
SANS = os.path.join(FONT_DIR, "DejaVuSans.ttf")
SANS_BOLD = os.path.join(FONT_DIR, "DejaVuSans-Bold.ttf")

W, H = 900, 1200

WARNING_CANONICAL = (
    "GOVERNMENT WARNING: (1) According to the Surgeon General, women should "
    "not drink alcoholic beverages during pregnancy because of the risk of "
    "birth defects. (2) Consumption of alcoholic beverages impairs your "
    "ability to drive a car or operate machinery, and may cause health "
    "problems."
)
WARNING_TITLECASE = (
    "Government Warning: (1) According to the Surgeon General, women should "
    "not drink alcoholic beverages during pregnancy because of the risk of "
    "birth defects. (2) Consumption of alcoholic beverages impairs your "
    "ability to drive a car or operate machinery, and may cause health "
    "problems."
)


def wrap_draw(draw, text, font, x, y, max_width, fill, line_spacing=6, bold_prefix=None):
    words = text.split(" ")
    lines, current = [], ""
    for w in words:
        trial = (current + " " + w).strip()
        if draw.textlength(trial, font=font) <= max_width:
            current = trial
        else:
            lines.append(current)
            current = w
    if current:
        lines.append(current)
    for line in lines:
        draw.text((x, y), line, font=font, fill=fill)
        y += font.size + line_spacing
    return y


def render_label(path, brand, class_type, abv, net, warning_text, warning_bold_all_caps=True,
                  bg=(250, 246, 235), accent=(60, 40, 20), blur=False, rotate=0, jpeg_quality=90):
    img = Image.new("RGB", (W, H), bg)
    draw = ImageDraw.Draw(img)

    # border
    draw.rectangle([20, 20, W - 20, H - 20], outline=accent, width=6)
    draw.rectangle([34, 34, W - 34, H - 34], outline=accent, width=2)

    brand_font = ImageFont.truetype(SERIF_BOLD, 64)
    class_font = ImageFont.truetype(SERIF, 34)
    field_font = ImageFont.truetype(SANS_BOLD, 30)
    warning_label_font = ImageFont.truetype(SANS_BOLD, 22) if warning_bold_all_caps else ImageFont.truetype(SANS, 22)
    warning_font = ImageFont.truetype(SANS, 20)

    y = 100
    y = wrap_draw(draw, brand, brand_font, 70, y, W - 140, accent, line_spacing=8)
    y += 20
    y = wrap_draw(draw, class_type, class_font, 70, y, W - 140, accent, line_spacing=6)
    y += 40

    draw.line([70, y, W - 70, y], fill=accent, width=2)
    y += 30

    draw.text((70, y), abv, font=field_font, fill=accent)
    y += 50
    draw.text((70, y), net, font=field_font, fill=accent)
    y += 50
    draw.text((70, y), "Distilled and Bottled by Example Producer, Louisville, KY", font=ImageFont.truetype(SANS, 18), fill=accent)
    y += 60

    draw.line([70, y, W - 70, y], fill=accent, width=2)
    y += 30

    # government warning block, near the bottom
    warn_y = H - 260
    y = wrap_draw(draw, warning_text, warning_font, 70, warn_y, W - 140, accent, line_spacing=6)

    if rotate:
        img = img.rotate(rotate, expand=True, fillcolor=bg)
    if blur:
        img = img.filter(ImageFilter.GaussianBlur(radius=3.5))
        # simulate low light / glare
        overlay = Image.new("RGB", img.size, (255, 255, 255))
        img = Image.blend(img, overlay, 0.12)

    img = img.convert("RGB")
    img.save(path, "JPEG", quality=jpeg_quality)


cases = []

# 1. Perfect match — bourbon
render_label(
    f"{OUT_DIR}/01_perfect_match_bourbon.jpg",
    "OLD TOM DISTILLERY", "Kentucky Straight Bourbon Whiskey",
    "45% Alc./Vol. (90 Proof)", "750 mL", WARNING_CANONICAL,
)
cases.append(dict(
    filename="01_perfect_match_bourbon.jpg",
    brand_name="Old Tom Distillery", class_type="Kentucky Straight Bourbon Whiskey",
    alcohol_content="45% Alc./Vol. (90 Proof)", net_contents="750 mL",
    government_warning=WARNING_CANONICAL,
    expected="ALL PASS — baseline case, everything matches exactly.",
))

# 2. Brand casing variant — Dave's "STONE'S THROW" example
render_label(
    f"{OUT_DIR}/02_brand_casing_variant.jpg",
    "STONE'S THROW", "American Craft Gin",
    "42% Alc./Vol.", "750 mL", WARNING_CANONICAL,
)
cases.append(dict(
    filename="02_brand_casing_variant.jpg",
    brand_name="Stone's Throw", class_type="American Craft Gin",
    alcohol_content="42% Alc./Vol.", net_contents="750 mL",
    government_warning=WARNING_CANONICAL,
    expected="ALL PASS — brand is all-caps on label vs title case on application; "
             "fuzzy match should treat this as the same brand (Dave's interview example).",
))

# 3. Warning title-case — Jenny's rejection example
render_label(
    f"{OUT_DIR}/03_warning_titlecase_fail.jpg",
    "HARVEST MOON CIDERY", "Hard Apple Cider",
    "6% Alc./Vol.", "500 mL", WARNING_TITLECASE, warning_bold_all_caps=False,
)
cases.append(dict(
    filename="03_warning_titlecase_fail.jpg",
    brand_name="Harvest Moon Cidery", class_type="Hard Apple Cider",
    alcohol_content="6% Alc./Vol.", net_contents="500 mL",
    government_warning=WARNING_CANONICAL,
    expected="government_warning FAILS (label prints it in Title Case, not ALL CAPS) "
             "-> overall FAIL, even though every other field matches. Jenny's interview example.",
))

# 4. ABV mismatch
render_label(
    f"{OUT_DIR}/04_abv_mismatch.jpg",
    "COASTAL POINT DISTILLING", "Silver Rum",
    "40% Alc./Vol.", "750 mL", WARNING_CANONICAL,
)
cases.append(dict(
    filename="04_abv_mismatch.jpg",
    brand_name="Coastal Point Distilling", class_type="Silver Rum",
    alcohol_content="45% Alc./Vol.", net_contents="750 mL",
    government_warning=WARNING_CANONICAL,
    expected="alcohol_content FAILS — label reads 40%%, application says 45%% "
             "(5 points, outside the +/-0.5 tolerance) -> overall FAIL.",
))

# 5. Proof notation only, should still match application's % notation
render_label(
    f"{OUT_DIR}/05_proof_notation_match.jpg",
    "REDWOOD WHISKEY CO.", "Straight Rye Whiskey",
    "90 Proof", "750 mL", WARNING_CANONICAL,
)
cases.append(dict(
    filename="05_proof_notation_match.jpg",
    brand_name="Redwood Whiskey Co.", class_type="Straight Rye Whiskey",
    alcohol_content="45% Alc./Vol.", net_contents="750 mL",
    government_warning=WARNING_CANONICAL,
    expected="ALL PASS — label only prints '90 Proof', application states '45%%'; "
             "these are numerically the same (proof / 2 = ABV%%).",
))

# 6. Net contents unit variant (L vs mL) — should pass
render_label(
    f"{OUT_DIR}/06_net_contents_unit_variant.jpg",
    "BLUE HERON BREWING", "Pilsner",
    "5% Alc./Vol.", "1 L", WARNING_CANONICAL,
)
cases.append(dict(
    filename="06_net_contents_unit_variant.jpg",
    brand_name="Blue Heron Brewing", class_type="Pilsner",
    alcohol_content="5% Alc./Vol.", net_contents="1000 mL",
    government_warning=WARNING_CANONICAL,
    expected="ALL PASS — label prints '1 L', application says '1000 mL'; same volume, different units.",
))

# 7. Net contents real mismatch
render_label(
    f"{OUT_DIR}/07_net_contents_mismatch.jpg",
    "IRONGATE DISTILLERY", "London Dry Gin",
    "47% Alc./Vol.", "375 mL", WARNING_CANONICAL,
)
cases.append(dict(
    filename="07_net_contents_mismatch.jpg",
    brand_name="Irongate Distillery", class_type="London Dry Gin",
    alcohol_content="47% Alc./Vol.", net_contents="750 mL",
    government_warning=WARNING_CANONICAL,
    expected="net_contents FAILS — label reads 375 mL (a half bottle), application says 750 mL -> overall FAIL.",
))

# 8. Brand mismatch (wrong label attached to wrong application entirely)
render_label(
    f"{OUT_DIR}/08_brand_major_mismatch.jpg",
    "NORTHFIELD DISTILLERY", "Vodka",
    "40% Alc./Vol.", "750 mL", WARNING_CANONICAL,
)
cases.append(dict(
    filename="08_brand_major_mismatch.jpg",
    brand_name="Sagebrush Spirits Co.", class_type="Vodka",
    alcohol_content="40% Alc./Vol.", net_contents="750 mL",
    government_warning=WARNING_CANONICAL,
    expected="brand_name FAILS — completely different brand text (simulates the wrong label "
             "attached to an application) -> overall FAIL.",
))

# 9. Low quality / blurry photo, otherwise a perfect match
render_label(
    f"{OUT_DIR}/09_low_quality_blurry.jpg",
    "SUNDOWN RIDGE WINERY", "Cabernet Sauvignon",
    "13.5% Alc./Vol.", "750 mL", WARNING_CANONICAL,
    blur=True, rotate=4, jpeg_quality=55,
)
cases.append(dict(
    filename="09_low_quality_blurry.jpg",
    brand_name="Sundown Ridge Winery", class_type="Cabernet Sauvignon",
    alcohol_content="13.5% Alc./Vol.", net_contents="750 mL",
    government_warning=WARNING_CANONICAL,
    expected="Best-effort case (Jenny's 'photographed at weird angles / bad lighting' example) — "
             "content matches if legible; exercises the low-confidence -> needs_review path if the "
             "model can't read it cleanly. Not a strict pass/fail expectation.",
))

# 10. Wine, full match, for beverage-type diversity
render_label(
    f"{OUT_DIR}/10_wine_full_match.jpg",
    "SUNSET RIDGE VINEYARDS", "Chardonnay",
    "13% Alc./Vol.", "750 mL", WARNING_CANONICAL,
)
cases.append(dict(
    filename="10_wine_full_match.jpg",
    brand_name="Sunset Ridge Vineyards", class_type="Chardonnay",
    alcohol_content="13% Alc./Vol.", net_contents="750 mL",
    government_warning=WARNING_CANONICAL,
    expected="ALL PASS — wine label, full match, covers a different beverage type than the bourbon baseline.",
))

# 11. Beer, full match, for beverage-type diversity
render_label(
    f"{OUT_DIR}/11_beer_full_match.jpg",
    "HOPYARD BREWING CO.", "India Pale Ale",
    "6.2% Alc./Vol.", "12 FL OZ", WARNING_CANONICAL,
)
cases.append(dict(
    filename="11_beer_full_match.jpg",
    brand_name="Hopyard Brewing Co.", class_type="India Pale Ale",
    alcohol_content="6.2% Alc./Vol.", net_contents="12 FL OZ",
    government_warning=WARNING_CANONICAL,
    expected="ALL PASS — beer label, full match, covers the third beverage type (beer/wine/spirits).",
))

manifest_path = f"{OUT_DIR}/manifest.csv"
with open(manifest_path, "w", newline="") as f:
    fieldnames = ["filename", "brand_name", "class_type", "alcohol_content", "net_contents", "government_warning"]
    writer = csv.DictWriter(f, fieldnames=fieldnames)
    writer.writeheader()
    for c in cases:
        writer.writerow({k: c[k] for k in fieldnames})

expectations_path = f"{OUT_DIR}/EXPECTATIONS.md"
with open(expectations_path, "w") as f:
    f.write("# Test Label Expectations\n\n")
    f.write(
        "Synthetic labels rendered with PIL (not an external image generator) so the "
        "ground-truth text is known exactly. Each case targets a specific rule or edge "
        "case from the take-home's interview notes. `manifest.csv` in this directory has "
        "the application-side data to submit alongside each image via the batch upload page.\n\n"
    )
    for c in cases:
        f.write(f"## {c['filename']}\n\n{c['expected']}\n\n")

print(f"Wrote {len(cases)} labels + manifest.csv + EXPECTATIONS.md to {OUT_DIR}")
