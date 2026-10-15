"""检查 README 下载链接和租房空白模板，仅使用 Python 标准库。"""

import re
from pathlib import Path
from xml.etree import ElementTree as ET
from zipfile import ZipFile


NS = {"s": "http://schemas.openxmlformats.org/spreadsheetml/2006/main"}


def require(condition, message):
    if not condition:
        raise ValueError(message)


def check_template(root):
    readme = (root / "README.md").read_text(encoding="utf-8")
    links = re.findall(r"\[[^\]]*\]\(([^)]+\.xlsx)\)", readme)
    require(len(links) == 1, "README 应包含一个 Excel 模板下载链接")
    template = root / links[0]
    require(template.is_file(), f"模板下载链接指向的文件不存在：{links[0]}")

    with ZipFile(template) as archive:
        require(archive.testzip() is None, "XLSX 压缩包完整性检查失败")
        names = archive.namelist()
        for name in ("[Content_Types].xml", "_rels/.rels", "xl/workbook.xml"):
            require(name in names, f"XLSX 缺少必要文件：{name}")
        require(
            not any("vbaproject" in name.lower() for name in names),
            "模板不应包含宏",
        )
        require(
            not any(name.startswith("xl/externalLinks/") for name in names),
            "模板不应包含外部工作簿链接",
        )
        documents = {
            name: ET.fromstring(archive.read(name))
            for name in names
            if name.endswith((".xml", ".rels"))
        }

    workbook = documents["xl/workbook.xml"]
    require(len(workbook.findall("s:sheets/s:sheet", NS)) == 1, "模板应只有一张工作表")
    sheets = [
        doc for name, doc in documents.items()
        if name.startswith("xl/worksheets/") and name.endswith(".xml")
    ]
    require(len(sheets) == 1, "XLSX 应包含一份工作表 XML")
    sheet = sheets[0]
    require(sheet.tag == f"{{{NS['s']}}}worksheet", "工作表 XML 格式不正确")
    require(sheet.find(".//s:f", NS) is None, "模板不应包含公式")

    strings = documents.get("xl/sharedStrings.xml")
    shared_strings = [] if strings is None else [
        "".join(item.itertext()) for item in strings.findall("s:si", NS)
    ]
    labels = {}
    for cell in sheet.findall(".//s:sheetData/s:row/s:c", NS):
        address = cell.get("r", "")
        value = cell.findtext("s:v", default="", namespaces=NS)
        if cell.get("t") == "s" and value:
            value = shared_strings[int(value)]
        elif cell.get("t") == "inlineStr":
            value = "".join(cell.find("s:is", NS).itertext())
        if re.fullmatch(r"A[1-9]\d*", address):
            labels[int(address[1:])] = value.strip()
        elif re.fullmatch(r"B[1-9]\d*", address) and address != "B1":
            require(not value.strip(), f"填写区域应为空白：{address}")

    first_month = [row for row, label in labels.items() if label == "首月总共要付多少"]
    monthly = [row for row, label in labels.items() if label == "以后每个月要付多少钱"]
    require(len(first_month) == 1, "模板应包含一项“首月总共要付多少”")
    require(len(monthly) == 1, "模板应包含一项“以后每个月要付多少钱”")
    require(monthly[0] == first_month[0] + 1, "每月费用应紧接在首月费用下方")

    location = [row for row, label in labels.items() if label == "小区地点"]
    require(len(location) == 1, "模板应包含一项“小区地点”")
    for offset, label in enumerate(("房间大小/平方", "朝向", "中介姓名"), start=1):
        rows = [row for row, value in labels.items() if value == label]
        require(rows == [location[0] + offset], f"“{label}”的位置不正确，应按小区地点、房间大小/平方、朝向、中介姓名排列")

    styles = documents["xl/styles.xml"]
    fonts = styles.findall("s:fonts/s:font", NS)
    cell_styles = styles.findall("s:cellXfs/s:xf", NS)
    for number in (location[0] + 1, location[0] + 2):
        row = sheet.find(f"s:sheetData/s:row[@r='{number}']", NS)
        for column, index in (("A", 1), ("B", 2)):
            address = f"{column}{number}"
            cell = row.find(f"s:c[@r='{address}']", NS)
            column_style = next((col.get("style", "0") for col in sheet.findall("s:cols/s:col", NS)
                                 if int(col.get("min")) <= index <= int(col.get("max"))), "0")
            style_id = row.get("s", column_style)
            if cell is not None:
                style_id = cell.get("s", style_id)
            font = fonts[int(cell_styles[int(style_id)].get("fontId", "0"))]
            bold = font.find("s:b", NS)
            require(bold is None or bold.get("val", "1") in ("0", "false"), f"新增两行不应加粗：{address}")
            if cell is not None:
                text = cell.find("s:is", NS)
                if cell.get("t") == "s":
                    text = strings.findall("s:si", NS)[int(cell.findtext("s:v", namespaces=NS))]
                if text is not None:
                    require(all(mark.get("val", "1") in ("0", "false") for mark in text.findall("s:r/s:rPr/s:b", NS)),
                            f"新增两行的文字不应加粗：{address}")

    print(f"检查通过：{links[0]}")


if __name__ == "__main__":
    check_template(Path(__file__).resolve().parents[2])
