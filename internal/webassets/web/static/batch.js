// Batch page: lets an agent either (a) fill in application data per label
// right on the page, with the filename read straight from the selected
// image files so there's nothing to type or mismatch, or (b) upload a CSV
// manifest for genuine bulk workflows where the data already exists in a
// spreadsheet. Both paths end up as the same "manifest" CSV file part that
// the existing /api/verify/batch endpoint already expects and handles
// unchanged — this file only builds that CSV client-side in row mode; the
// actual submission is still handled entirely by htmx (hx-post etc. on
// #batch-form, untouched), not reimplemented here.
//
// Design note: the hidden manifest input is kept in sync continuously
// (on every row edit / file selection), not "prepared just before
// submit". That sidesteps any question of whether this script's listeners
// run before or after htmx's own submit handling -- by the time a submit
// happens, the data this script owns is simply already correct.
(function () {
  "use strict";

  const imagesInput = document.getElementById("label_images");
  const rowsSection = document.getElementById("rows-section");
  const rowsContainer = document.getElementById("per-image-rows");
  const csvSection = document.getElementById("csv-upload-section");
  const csvInput = document.getElementById("manifest_csv_input");
  const hiddenManifestInput = document.getElementById("manifest_hidden");
  const toggleToCsv = document.getElementById("toggle-csv-mode");
  const toggleToRows = document.getElementById("toggle-row-mode");

  const MANIFEST_FIELDS = ["brand_name", "class_type", "alcohol_content", "net_contents"];
  const FIELD_LABELS = {
    brand_name: "Brand Name",
    class_type: "Class / Type",
    alcohol_content: "Alcohol Content",
    net_contents: "Net Contents",
  };
  const FIELD_PLACEHOLDERS = {
    alcohol_content: "e.g. 45% Alc./Vol. (90 Proof)",
    net_contents: "e.g. 750 mL",
  };

  let mode = "rows";

  function setMode(newMode) {
    mode = newMode;
    if (mode === "rows") {
      rowsSection.hidden = false;
      csvSection.hidden = true;
      hiddenManifestInput.name = "manifest";
      csvInput.name = "";
      csvInput.required = false;
      syncHiddenManifestFromRows();
    } else {
      rowsSection.hidden = true;
      csvSection.hidden = false;
      csvInput.name = "manifest";
      csvInput.required = true;
      hiddenManifestInput.name = "";
    }
  }

  // Renders one row of inputs per selected image, filename taken directly
  // from the File object -- never typed, so it can never drift from what's
  // actually being uploaded.
  function renderRows() {
    rowsContainer.textContent = "";
    const files = imagesInput.files || [];

    if (files.length === 0) {
      const p = document.createElement("p");
      p.className = "help";
      p.textContent = "Select label images above to fill in details for each one here.";
      rowsContainer.appendChild(p);
      return;
    }

    for (const file of files) {
      const row = document.createElement("fieldset");
      row.className = "manifest-row";
      row.dataset.filename = file.name;

      const legend = document.createElement("legend");
      legend.textContent = file.name;
      row.appendChild(legend);

      for (const field of MANIFEST_FIELDS) {
        const label = document.createElement("label");
        label.textContent = FIELD_LABELS[field];

        const input = document.createElement("input");
        input.type = "text";
        input.className = "row-" + field;
        input.required = true;
        if (FIELD_PLACEHOLDERS[field]) {
          input.placeholder = FIELD_PLACEHOLDERS[field];
        }
        input.addEventListener("input", syncHiddenManifestFromRows);

        label.appendChild(input);
        row.appendChild(label);
      }

      rowsContainer.appendChild(row);
    }
  }

  // RFC 4180 CSV field escaping, matching what the server's
  // encoding/csv-based parser expects.
  function csvEscape(value) {
    const s = String(value == null ? "" : value);
    if (/[",\r\n]/.test(s)) {
      return '"' + s.replace(/"/g, '""') + '"';
    }
    return s;
  }

  function buildManifestCSV() {
    const lines = [["filename"].concat(MANIFEST_FIELDS).map(csvEscape).join(",")];
    const rows = rowsContainer.querySelectorAll(".manifest-row");
    for (const row of rows) {
      const values = [row.dataset.filename];
      for (const field of MANIFEST_FIELDS) {
        const input = row.querySelector(".row-" + field);
        values.push(input ? input.value : "");
      }
      lines.push(values.map(csvEscape).join(","));
    }
    return lines.join("\r\n") + "\r\n";
  }

  function syncHiddenManifestFromRows() {
    if (mode !== "rows") {
      return;
    }
    const csvText = buildManifestCSV();
    const file = new File([csvText], "manifest.csv", { type: "text/csv" });
    const dt = new DataTransfer();
    dt.items.add(file);
    hiddenManifestInput.files = dt.files;
  }

  imagesInput.addEventListener("change", function () {
    renderRows();
    syncHiddenManifestFromRows();
  });

  toggleToCsv.addEventListener("click", function (e) {
    e.preventDefault();
    setMode("csv");
  });
  toggleToRows.addEventListener("click", function (e) {
    e.preventDefault();
    setMode("rows");
  });

  setMode("rows");
})();
