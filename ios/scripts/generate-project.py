#!/usr/bin/env python3
"""Generate the small native project from checked-in source lists; no XcodeGen dependency."""
from pathlib import Path
import hashlib

root = Path(__file__).resolve().parent.parent
objects = []


def uid(name):
    return hashlib.sha1(name.encode()).hexdigest()[:24].upper()


def obj(name, body):
    objects.append(f"\t\t{uid(name)} = {{ {body} }};")
    return uid(name)


def sources(folder):
    references, builds = [], []
    for file in sorted((root / folder).glob("*.swift")):
        reference = obj("ref:" + folder + file.name, f'isa = PBXFileReference; lastKnownFileType = sourcecode.swift; path = {file.name}; sourceTree = "<group>";')
        references.append(reference)
        builds.append(obj("build:" + folder + file.name, f"isa = PBXBuildFile; fileRef = {reference};"))
    return references, builds


apprefs, appbuilds = sources("App")
testrefs, testbuilds = sources("UITests")
privacy = obj("privacyref", 'isa = PBXFileReference; lastKnownFileType = text.xml; path = PrivacyInfo.xcprivacy; sourceTree = "<group>";')
apprefs.append(privacy)
privacybuild = obj("privacybuild", f"isa = PBXBuildFile; fileRef = {privacy};")
product = obj("product", 'isa = PBXFileReference; explicitFileType = wrapper.application; path = JobmanDashboard.app; sourceTree = BUILT_PRODUCTS_DIR;')
testproduct = obj("testproduct", 'isa = PBXFileReference; explicitFileType = wrapper.cfbundle; path = DashboardUITests.xctest; sourceTree = BUILT_PRODUCTS_DIR;')
appgroup = obj("appgroup", f'isa = PBXGroup; children = ({", ".join(apprefs)},); path = App; sourceTree = "<group>";')
testgroup = obj("testgroup", f'isa = PBXGroup; children = ({", ".join(testrefs)},); path = UITests; sourceTree = "<group>";')
products = obj("products", f'isa = PBXGroup; children = ({product}, {testproduct},); name = Products; sourceTree = "<group>";')
configs = [obj("configref:" + mode, f'isa = PBXFileReference; lastKnownFileType = text.xcconfig; path = {mode}.xcconfig; sourceTree = "<group>";') for mode in ["Debug", "Release"]]
configgroup = obj("configgroup", f'isa = PBXGroup; children = ({", ".join(configs)},); path = Config; sourceTree = "<group>";')
main = obj("main", f'isa = PBXGroup; children = ({appgroup}, {testgroup}, {configgroup}, {products},); sourceTree = "<group>";')
package = obj("package", 'isa = XCLocalSwiftPackageReference; relativePath = .;')
packageproduct = obj("packageproduct", f"isa = XCSwiftPackageProductDependency; package = {package}; productName = DashboardCore;")
packagelink = obj("packagelink", f"isa = PBXBuildFile; productRef = {packageproduct};")


def phase(name, kind, files):
    return obj(name, f'isa = PBX{kind}BuildPhase; buildActionMask = 2147483647; files = ({", ".join(files)}); runOnlyForDeploymentPostprocessing = 0;')


appphases = [phase("sources", "Sources", appbuilds), phase("frameworks", "Frameworks", [packagelink]), phase("resources", "Resources", [privacybuild])]
testphases = [phase("testsources", "Sources", testbuilds), phase("testframeworks", "Frameworks", [])]
projectconfigs, targetconfigs, testconfigs = [], [], []
for index, mode in enumerate(["Debug", "Release"]):
    active_arch = "YES" if mode == "Debug" else "NO"
    projectconfigs.append(obj("projectconfig:" + mode, f"isa = XCBuildConfiguration; buildSettings = {{ CLANG_ENABLE_MODULES = YES; SDKROOT = iphoneos; IPHONEOS_DEPLOYMENT_TARGET = 18.0; SWIFT_VERSION = 6.0; SWIFT_STRICT_CONCURRENCY = complete; ONLY_ACTIVE_ARCH = {active_arch}; }}; name = {mode};"))
    settings = 'CODE_SIGN_STYLE = Automatic; CODE_SIGN_ENTITLEMENTS = Config/Dashboard.entitlements; GENERATE_INFOPLIST_FILE = NO; INFOPLIST_FILE = Config/Info.plist; PRODUCT_BUNDLE_IDENTIFIER = org.jobman.dashboard; PRODUCT_NAME = "$(TARGET_NAME)"; TARGETED_DEVICE_FAMILY = 1; SUPPORTED_PLATFORMS = "iphoneos iphonesimulator"; SUPPORTS_MACCATALYST = NO; SUPPORTS_MAC_DESIGNED_FOR_IPHONE_IPAD = NO; CURRENT_PROJECT_VERSION = 1; MARKETING_VERSION = 0.1.0; ENABLE_USER_SCRIPT_SANDBOXING = YES;'
    settings += ' SWIFT_OPTIMIZATION_LEVEL = "-Onone"; DEBUG_INFORMATION_FORMAT = dwarf;' if mode == "Debug" else ' SWIFT_COMPILATION_MODE = wholemodule; DEBUG_INFORMATION_FORMAT = "dwarf-with-dsym";'
    targetconfigs.append(obj("targetconfig:" + mode, f"isa = XCBuildConfiguration; baseConfigurationReference = {configs[index]}; buildSettings = {{ {settings} }}; name = {mode};"))
    testconfigs.append(obj("testconfig:" + mode, f'isa = XCBuildConfiguration; buildSettings = {{ CODE_SIGN_STYLE = Automatic; GENERATE_INFOPLIST_FILE = YES; PRODUCT_BUNDLE_IDENTIFIER = org.jobman.dashboard.uitests; PRODUCT_NAME = "$(TARGET_NAME)"; TEST_TARGET_NAME = JobmanDashboard; TARGETED_DEVICE_FAMILY = 1; }}; name = {mode};'))


def configlist(name, values):
    return obj(name, f'isa = XCConfigurationList; buildConfigurations = ({", ".join(values)},); defaultConfigurationIsVisible = 0; defaultConfigurationName = Release;')


projectlist, targetlist, testlist = configlist("projectlist", projectconfigs), configlist("targetlist", targetconfigs), configlist("testlist", testconfigs)
target = obj("target", f'isa = PBXNativeTarget; buildConfigurationList = {targetlist}; buildPhases = ({", ".join(appphases)},); buildRules = (); dependencies = (); name = JobmanDashboard; packageProductDependencies = ({packageproduct},); productName = JobmanDashboard; productReference = {product}; productType = "com.apple.product-type.application";')
proxy = obj("testproxy", f'isa = PBXContainerItemProxy; containerPortal = {uid("project")}; proxyType = 1; remoteGlobalIDString = {target}; remoteInfo = JobmanDashboard;')
dependency = obj("testdependency", f"isa = PBXTargetDependency; target = {target}; targetProxy = {proxy};")
testtarget = obj("testtarget", f'isa = PBXNativeTarget; buildConfigurationList = {testlist}; buildPhases = ({", ".join(testphases)},); buildRules = (); dependencies = ({dependency},); name = DashboardUITests; productName = DashboardUITests; productReference = {testproduct}; productType = "com.apple.product-type.bundle.ui-testing";')
project = obj("project", f'isa = PBXProject; attributes = {{ BuildIndependentTargetsInParallel = YES; LastUpgradeCheck = 1600; TargetAttributes = {{ {testtarget} = {{ TestTargetID = {target}; }}; }}; }}; buildConfigurationList = {projectlist}; compatibilityVersion = "Xcode 15.0"; developmentRegion = en; hasScannedForEncodings = 0; knownRegions = (en, Base,); mainGroup = {main}; packageReferences = ({package},); productRefGroup = {products}; projectDirPath = ""; projectRoot = ""; targets = ({target}, {testtarget},);')
proj = root / "JobmanDashboard.xcodeproj"
proj.mkdir(exist_ok=True)
(proj / "project.pbxproj").write_text('// !$*UTF8*$!\n{\n\tarchiveVersion = 1;\n\tclasses = {};\n\tobjectVersion = 60;\n\tobjects = {\n' + '\n'.join(objects) + f'\n\t}};\n\trootObject = {project};\n}}\n')
schemes = proj / "xcshareddata/xcschemes"
schemes.mkdir(parents=True, exist_ok=True)


def reference(identifier, name, product):
    return f'<BuildableReference BuildableIdentifier="primary" BlueprintIdentifier="{identifier}" BuildableName="{product}" BlueprintName="{name}" ReferencedContainer="container:JobmanDashboard.xcodeproj"/>'


ref, testref = reference(target, "JobmanDashboard", "JobmanDashboard.app"), reference(testtarget, "DashboardUITests", "DashboardUITests.xctest")
(schemes / "JobmanDashboard.xcscheme").write_text(f'''<?xml version="1.0" encoding="UTF-8"?>
<Scheme LastUpgradeVersion="1600" version="1.3">
  <BuildAction parallelizeBuildables="YES" buildImplicitDependencies="YES"><BuildActionEntries><BuildActionEntry buildForTesting="YES" buildForRunning="YES" buildForProfiling="YES" buildForArchiving="YES" buildForAnalyzing="YES">{ref}</BuildActionEntry></BuildActionEntries></BuildAction>
  <TestAction buildConfiguration="Debug" selectedDebuggerIdentifier="Xcode.DebuggerFoundation.Debugger.LLDB" selectedLauncherIdentifier="Xcode.IDEFoundation.Launcher.LLDB" shouldUseLaunchSchemeArgsEnv="YES"><Testables><TestableReference skipped="NO">{testref}</TestableReference></Testables></TestAction>
  <LaunchAction buildConfiguration="Debug" selectedDebuggerIdentifier="Xcode.DebuggerFoundation.Debugger.LLDB" selectedLauncherIdentifier="Xcode.IDEFoundation.Launcher.LLDB" launchStyle="0" useCustomWorkingDirectory="NO" ignoresPersistentStateOnLaunch="NO" debugDocumentVersioning="YES" debugServiceExtension="internal" allowLocationSimulation="YES"><BuildableProductRunnable runnableDebuggingMode="0">{ref}</BuildableProductRunnable></LaunchAction>
  <ProfileAction buildConfiguration="Release" shouldUseLaunchSchemeArgsEnv="YES" savedToolIdentifier="" useCustomWorkingDirectory="NO" debugDocumentVersioning="YES"><BuildableProductRunnable runnableDebuggingMode="0">{ref}</BuildableProductRunnable></ProfileAction>
  <AnalyzeAction buildConfiguration="Debug"/>
  <ArchiveAction buildConfiguration="Release" revealArchiveInOrganizer="YES"/>
</Scheme>
''')
