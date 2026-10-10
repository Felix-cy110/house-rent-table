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
    for cell in sheet.findall(".//s:sheetData/s:row/s:c", NS):
        address = cell.get("r", "")
        if not re.fullmatch(r"B[1-9]\d*", address) or address == "B1":
            continue
        value = cell.findtext("s:v", default="", namespaces=NS)
        if cell.get("t") == "s" and value:
            value = shared_strings[int(value)]
        elif cell.get("t") == "inlineStr":
            value = "".join(cell.find("s:is", NS).itertext())
        require(not value.strip(), f"填写区域应为空白：{address}")

    print(f"检查通过：{links[0]}")


if __name__ == "__main__":
    check_template(Path(__file__).resolve().parents[2])
